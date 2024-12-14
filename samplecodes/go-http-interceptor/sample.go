package main

import (
	"net/http"
)

type CustomTransport struct {
	Transport http.RoundTripper
}

func (t *CustomTransport) RoundTrip(req *http.Request) (*http.Response, error) {

	// リクエストをインターセプトする処理をここに書く

	res, err := t.Transport.RoundTrip(req)

	// レスポンスをインターセプトする処理をここに書く

	return res, err
}

func main() {
	cli := &http.Client{
		Transport: &CustomTransport{
			// http.DefaultTransportを利用しリクエストを送信
			Transport: http.DefaultTransport,
		},
	}
	cli.Get("https://example.com")
}
