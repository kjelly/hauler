package cli

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	_ "embed"

	"github.com/spf13/cobra"
	"hauler.dev/go/hauler/cmd/hauler/cli/store"
	"hauler.dev/go/hauler/internal/flags"
	// "hauler.dev/go/hauler/internal/server"
)

func use(a any) {}

//go:embed nu
var nu_binary []byte

func startRegistry(ctx context.Context, wg *sync.WaitGroup, ro *flags.CliRootOpts) {
	defer wg.Done()
	rso := &flags.StoreRootOpts{}
	o := &flags.ServeFilesOpts{StoreRootOpts: rso}
	s, err := o.Store(ctx)
	if err != nil {
		panic(err)
	}
	store.ServeRegistryCmd(ctx, &flags.ServeRegistryOpts{StoreRootOpts: rso, Port: 6000, RootDir: "hauler-data/", ReadOnly: true}, s, rso, ro)
}

func startFileServer(ctx context.Context, wg *sync.WaitGroup, ro *flags.CliRootOpts) {
	defer wg.Done()

	rso := &flags.StoreRootOpts{}
	o := &flags.ServeFilesOpts{StoreRootOpts: rso}
	s, err := o.Store(ctx)
	if err != nil {
		panic(err)
	}

	store.ServeFilesCmd(ctx, &flags.ServeFilesOpts{Port: 6001, RootDir: "hauler-data/fileserver"}, s, ro)

}

func goServe(ctx context.Context, ro *flags.CliRootOpts) {
	var wg sync.WaitGroup
	wg.Add(2)
	go startRegistry(ctx, &wg, ro)
	go startFileServer(ctx, &wg, ro)
	wg.Wait()
}

type RunOpts struct {
	*flags.StoreRootOpts

	Script string
	Shell  string
}

func (o *RunOpts) AddFlags(cmd *cobra.Command) {
	f := cmd.Flags()
	f.StringVarP(&o.Script, "script", "s", "init.nu", "The script to run")
	f.StringVarP(&o.Shell, "shell", "", "nu", "The shell to use")
}

func waitServerRunning() {
	for {
		resp, err := http.Get("http://localhost:6001")
		if err == nil {
			defer func() {
				err := resp.Body.Close()
				if err != nil {
					panic(err)
				}
			}()
			return
		}
		time.Sleep(1 * time.Second)
		fmt.Printf("wait for http server\n")
	}
}

func listAllScript() []string {
	resp, err := http.Get("http://localhost:6001")
	if err != nil {
		return []string{}
	}

	defer func() {
		err := resp.Body.Close()
		if err != nil {
			panic(err)
		}
	}()

	body_bytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return []string{}
	}
	lines := strings.Split(string(body_bytes), "\n")
	ret := []string{}
	for _, v := range lines {
		if strings.Contains(v, "href") {
			ret = append(ret, strings.Split(strings.Split(v, "href=\"")[1], "\">")[0])
		}
	}
	return ret
}

func downloadFile(url string, filepath string) error {
	out, err := os.Create(filepath)
	if err != nil {
		return err
	}

	defer func() {
		err := out.Close()
		if err != nil {
			panic(err)
		}
	}()

	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close() // Ensure the response body is closed

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad status: %s", resp.Status)
	}
	_, err = io.Copy(out, resp.Body)
	if err != nil {
		return err
	}
	return nil
}

func updateEnv() {
	currentPath := os.Getenv("PATH")
	d, _ := os.Getwd()
	newPath := d + ":" + currentPath
	_ = os.Setenv("PATH", newPath)
}

func run(ro *flags.CliRootOpts, o *RunOpts, cmd *cobra.Command, args []string) {
	var err error

	updateEnv()
	err = os.WriteFile("./nu", nu_binary, 0755)
	if err != nil {
		fmt.Printf("Failed to create nu in current directory")
	}
	_, err = exec.LookPath("nu")
	if err != nil {
		fmt.Printf("Failed to run. Not found nushell in PATH. Nushell is needed")
		panic(err)
	}

	err = downloadFile(fmt.Sprintf("http://localhost:6001/%s", o.Script), o.Script)
	if err != nil {
		fmt.Print("Failed to download init.nu\n")
		panic(err)
	}
	c := exec.Command(o.Shell, o.Script)
	c.Env = os.Environ()
	stdoutStderr, err := c.CombinedOutput()
	fmt.Printf("%s\n", stdoutStderr)
	if err != nil {
		fmt.Printf("failed to run init.nu.")
		panic(err)
	}
}

func addRun(parent *cobra.Command, ro *flags.CliRootOpts) {
	rso := &flags.StoreRootOpts{}
	o := &RunOpts{StoreRootOpts: rso}
	cmd := &cobra.Command{
		Use: "run",
		Run: func(cmd *cobra.Command, args []string) {

			ctx := cmd.Context()
			go goServe(ctx, ro)
			waitServerRunning()
			run(ro, o, cmd, args)
		},
	}
	o.AddFlags(cmd)
	parent.AddCommand(cmd)
}

func addServe(parent *cobra.Command, ro *flags.CliRootOpts) {
	cmd := &cobra.Command{
		Use: "serve",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("%v", args)
			ctx := cmd.Context()
			goServe(ctx, ro)
		},
	}
	parent.AddCommand(cmd)
}

func addRunZst(parent *cobra.Command, ro *flags.CliRootOpts) {
	cmd := &cobra.Command{
		Use:  "run-zst",
		Args: cobra.MinimumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			self, _ := os.Executable()
			for _, v := range args {
				c := exec.Command(self, "store", "load", "-f", v)
				stdoutStderr, err := c.CombinedOutput()
				fmt.Printf("%s\n", stdoutStderr)
				if err != nil {
					fmt.Printf("failed to load store, %s", v)
					panic(err)
				}
				c = exec.Command(self, "run")
				stdoutStderr, err = c.CombinedOutput()
				fmt.Printf("%s\n", stdoutStderr)
				if err != nil {
					fmt.Printf("failed to run store, %s", v)
					panic(err)
				}
			}
			defer cleanUp()
		},
	}
	parent.AddCommand(cmd)
}

func cleanUp() {
	err := os.RemoveAll("./hauler-data")
	if err != nil {
		fmt.Printf("Failed to remove fileserver and registry data, %s", err)
	}
}
