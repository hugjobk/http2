package http2_test

import (
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	chttp2 "github.com/hugjobk/http2"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
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

var customClient = http.Client{
	Transport: &chttp2.Transport{
		MaxConnsPerHost: 10,
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

func init() {
	go initServer()
}

func Test1(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "https://"+SrvAddr, nil)
	resp, err := customClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	t.Log(string(b))
}

func doRequest(cli *http.Client) error {
	req, _ := http.NewRequest(http.MethodGet, "https://"+SrvAddr, nil)
	resp, err := cli.Do(req)
	if err != nil {
		return err
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return nil
}

func Benchmark1(b *testing.B) {
	b.SetParallelism(100)
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if err := doRequest(&stdClient); err != nil {
				b.Error(err)
			}
		}
	})
}

func Benchmark2(b *testing.B) {
	b.SetParallelism(100)
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if err := doRequest(&customClient); err != nil {
				b.Error(err)
			}
		}
	})
}
