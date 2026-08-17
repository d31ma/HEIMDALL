// fake-engine runs the test suite's fake Docker Engine as a standalone
// process, for demos and UI walkthroughs on machines with no daemon. It
// speaks enough of the Engine API for HEIMDALL to deploy, observe, and tail
// logs against it. State is in memory and dies with the process.
package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/d31ma/heimdall/internal/provider/docker/dockertest"
)

func main() {
	var engine *dockertest.Engine
	if len(os.Args) > 1 {
		// A fixed address keeps a registered target valid across restarts.
		fixed, err := dockertest.NewAt(os.Args[1])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		engine = fixed
	} else {
		engine = dockertest.New()
	}
	fmt.Println(engine.URL())
	// Stay up until killed; the URL on stdout is the contract.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	engine.Close()
}
