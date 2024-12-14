---
title: "Golangでhttp.Clientのインターセプターを作りたいあなたへ"
emoji: "🗂"
type: "tech" # tech: 技術記事 / idea: アイデア
topics: ["go"]
published: true
---


## この記事について
* Goでhttpクライアントを使っていた際に、Interceptorの実装サンプルが見つからなかったので書いてみました。
* ほぼ自分用スニペットですが、もし誰かのお役に立てれば幸いです。

## 結論

- `net/http` パッケージに含まれる `http.Client` の `Transport` フィールドに `http.RoundTripper` インターフェースを満たす自作の構造体をセットすることで、リクエストやレスポンスをインターセプトすることができます。
- HTTPリクエストを送るために、`http.DefaultTransport`をコールすることを忘れずに。

この記事で紹介するコードは下記のリンクに置いておきます。

https://github.com/katonium/articles/tree/main/samplecodes/go-http-interceptor/


```go::main.go
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
```

## 実装例①：リクエストをダンプするインターセプター

```go::logging-interceptor.go
type LoggingInterceptor struct {
	Transport http.RoundTripper
}

func (i *LoggingInterceptor) RoundTrip(req *http.Request) (*http.Response, error) {

	dump, err := httputil.DumpRequest(req, true)
	if err != nil {
		log.Printf("DumpRequest error - %+v", err)
		// DO NOT return when error occurs because it's not critical
	} else {
		log.Printf("DumpRequest = %s", string(dump))
	}

	resp, err := i.Transport.RoundTrip(req)
	if err != nil {
		// logs are not printed here because log is assumed to be printed in caller function
		return resp, err
	}

	dump, err = httputil.DumpResponse(resp, true)
	if err != nil {
		log.Printf("DumpResponse error - %+v", err)
		return resp, err
	}

	log.Printf("DumpResponse = %s", string(dump))
	return resp, err
}
```


## 実装例①：429と503の際にリトライするインターセプター

※エクスポネンシャルバックオフではなく、線形にスリープ時間を増やしてリトライするインターセプターです。

```go::retry-interceptor.go
// RetryInterceptor is a transport interceptor that retries the request
// when the response status code is 429 (Too Many Requests) or 503 (Service Unavailable).
type RetryInterceptor struct {
	Transport http.RoundTripper
}

// WithMaxAttempt is a helper function that retries the function when the error occurs.
func WithMaxAttempt(ctx context.Context, attempt int, f func() error) error {
	var err error
	for i := 0; i < attempt; i++ {
		err = f()
		if err == nil {
			return nil
		}
	}
	return fmt.Errorf("failed to execute function after %d attempts: %w", attempt, err)
}

func (i *RetryInterceptor) RoundTrip(req *http.Request) (*http.Response, error) {

	var resp *http.Response
	var err error
	sleepTime := 100 * time.Millisecond
	attempt := 0

	rerr := WithMaxAttempt(req.Context(), 3, func() error {
		log.Printf("attempt %d", attempt)
		attempt++

		resp, err = i.Transport.RoundTrip(req)
		if err != nil {
			// return nil to suppress retry when unexpected error occurs
			return nil
		}

		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable {
			// sleep with jitter
			time.Sleep(sleepTime)
			sleepTime = sleepTime + 2*time.Second
			return fmt.Errorf("http status code %d", resp.StatusCode) // return error to retry
		}
		return nil
	})
	if rerr != nil {
		err = rerr
	}
	if err != nil {
		return nil, fmt.Errorf("failed to send http request - %w", err)
	}
	return resp, nil
}
```


## 記事を書いた後に気づいたこと

「Go HTTP インターセプター」って調べると出ないけど「」って調べると先人の偉大な記事がいっぱい出てきたので、こちらもぜひご参照ください。

https://zenn.dev/fujisawa33/articles/aef6d266aa751f

https://qiita.com/tutuming/items/6006e1d8cf94bc40f8e8

https://rennnosukesann.hatenablog.com/entry/2024/08/03/162216

