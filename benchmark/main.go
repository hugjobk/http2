package main

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"os"
	"runtime"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"github.com/hugjobk/go-benchmark"
	hhttp2 "github.com/hugjobk/http2"
)

const SrvAddr = "127.0.0.1:8888"

var stdClient = http.Client{
	Transport: &http2.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		DialTLS: func(network, addr string, cfg *tls.Config) (net.Conn, error) {
			d := net.Dialer{Timeout: 1 * time.Second}
			return d.Dial(network, addr)
		},
	},
}

var myClient = http.Client{
	Transport: &hhttp2.Transport{
		MaxConnsPerHost: 100,
		IdleConnTimeout: 3 * time.Second,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
		},
		DialTLS: func(network, addr string, cfg *tls.Config) (net.Conn, error) {
			d := net.Dialer{Timeout: 1 * time.Second}
			return d.Dial(network, addr)
		},
	},
}

func handle(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	io.WriteString(w, "OK")
}

func initServer() {
	srv := http.Server{
		Addr:    SrvAddr,
		Handler: h2c.NewHandler(http.HandlerFunc(handle), &http2.Server{}),
	}
	if err := srv.ListenAndServe(); err != nil {
		panic(err)
	}
}

func doRequest(cli *http.Client) error {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+SrvAddr, nil)
	resp, err := cli.Do(req)
	if err != nil {
		return err
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return nil
}

func main() {
	go initServer()
	runtime.GOMAXPROCS(2)
	b := benchmark.Benchmark{
		WorkerCount:  500,
		Duration:     30 * time.Second,
		LatencyStart: 1 * time.Millisecond,
		LatencyStep:  6,
		// ShowProcess:  true,
	}
	b.Run("Benchmark standard client", func(i int) error {
		return doRequest(&stdClient)
	}).Report(os.Stdout).PrintErrors(os.Stdout, 100)
	stdClient.CloseIdleConnections()
	b.Run("Benchmark custom client", func(i int) error {
		return doRequest(&myClient)
	}).Report(os.Stdout).PrintErrors(os.Stdout, 100)
}
