package main

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"syscall"
	"time"
)

func main() {
	mode := "healthy"
	if len(os.Args) > 1 {
		mode = os.Args[1]
	}
	port := os.Getenv("PORT")
	addr := "127.0.0.1:" + port
	switch mode {
	case "healthy":
		http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
		must(http.ListenAndServe(addr, nil))
	case "never-healthy":
		must(http.ListenAndServe(addr, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) })))
	case "never-bind":
		select {}
	case "crash":
		os.Exit(17)
	case "fork":
		c := exec.Command(os.Args[0], "ignore")
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		must(c.Start())
		fmt.Println("child", c.Process.Pid)
		select {}
	case "ignore":
		signal.Ignore(syscall.SIGTERM)
		select {}
	case "noisy":
		n := 12000
		if v := os.Getenv("LINES"); v != "" {
			n, _ = strconv.Atoi(v)
		}
		for i := 0; i < n; i++ {
			fmt.Printf("stdout mir_secret %06d %s\n", i, string(make([]byte, 1024)))
			fmt.Fprintf(os.Stderr, "stderr Bearer secret %06d\n", i)
		}
		time.Sleep(50 * time.Millisecond)
	default:
		panic(mode)
	}
}
func must(err error) {
	if err != nil {
		panic(err)
	}
}
