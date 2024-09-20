// https://qiita.com/convto/items/64e8f090198a4cf7a4fc
package main

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
)

func main() {
	// backendServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	// 	// header 見たいので request の dump を response で返す
	// 	dump, err := httputil.DumpRequest(r, false)
	// 	if err != nil {
	// 		fmt.Fprintln(w, err)
	// 	}
	// 	fmt.Fprintln(w, string(dump))
	// }))
	// defer backendServer.Close()

	// rpURL, err := url.Parse(backendServer.URL)
	// rpURL, err := url.Parse("http://localhost:4444")
	rpURL, err := url.Parse("http://localhost:10001")
	if err != nil {
		log.Fatal(err)
	}

	director := func(req *http.Request) {
		req.URL.Scheme = rpURL.Scheme
		req.URL.Host = rpURL.Host
		// proxy 内部でヘッダに値を追加してみる
		req.Header.Set("X-Forwarded-Proto", rpURL.Scheme)
		req.Header.Set("X-Forwarded-Host", rpURL.Host)
		req.Header.Set("X-Forwarded-Server", rpURL.Host)

		// リクエストの dump を取得
		dump, err := httputil.DumpRequest(req, true)
		if err != nil {
			fmt.Printf("httputil.DumpRequest err - %+v", err)
			return
		}
		fmt.Printf("dump request: %s\n\n", string(dump))

	}
	modifyResponse := func(res *http.Response) error {
		// ヘッダー追加してみる
		// body が JSON などの構造化データであれば要素の追加などもかんたんにできると思います
		res.Header.Set("X-Test-Header", "test header data")

		dump, err := httputil.DumpResponse(res, true)
		if err != nil {
			fmt.Printf("httputil.DumpResponse err - %+v", err)
			return nil
		}
		fmt.Printf("dump response: %s\n\n", string(dump))
		return nil
	}

	rp := httputil.NewSingleHostReverseProxy(rpURL)
	rp.ModifyResponse = modifyResponse
	rp.Director = director

	frontendProxy := NewServer(rp)
	// frontendProxy := httptest.NewServer(&httputil.ReverseProxy{Director: director})
	defer frontendProxy.Close()

	<-make(chan struct{})
}

func call(url string) {
	// リクエスト定義
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		log.Fatal(err)
	}
	// Connection ヘッダー追加
	req.Header.Set("Connection", "keep-alive")
	resp, err := new(http.Client).Do(req)
	if err != nil {
		log.Fatal(err)
	}

	dump, err := httputil.DumpResponse(resp, true)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("%s", string(dump))
}

// NewServer starts and returns a new [Server].
// The caller should call Close when finished, to shut it down.
func NewServer(handler http.Handler) *httptest.Server {
	ts := NewUnstartedServer(handler)
	ts.Start()
	return ts
}

// NewUnstartedServer returns a new [Server] but doesn't start it.
//
// After changing its configuration, the caller should call Start or
// StartTLS.
//
// The caller should call Close when finished, to shut it down.
func NewUnstartedServer(handler http.Handler) *httptest.Server {
	addr := "127.0.0.1:4445"
	l, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("httptest: NewUnstartedServer: %v", err)
	}

	return &httptest.Server{
		Listener: l,
		Config:   &http.Server{Handler: handler},
	}
}
