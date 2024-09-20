---
title: "slack-go/slackでfiles.upload APIのduplicateにようやく備えた"
emoji: "🤥"
type: "tech" # tech: 技術記事 / idea: アイデア
topics: ["slack", "go", "tech"]
published: true
---

## この記事について

Slack APIのfiles.upload APIによるファイルのアップロードが非推奨になり、2025年3月11日までに別のAPIを利用したファイルのアップロード方法に切り替える必要があります。
筆者は趣味でGolangのアプリを書いているので、そのGolangアプリで使っている[slack-go/slack](https://pkg.go.dev/github.com/slack-go/slack)でこの対応をする必要があります。

https://api.slack.com/changelog/2024-04-a-better-way-to-upload-files-is-here-to-stay

**...というのは4月からずっと知っていたのですが**、ようやく対応したので実施内容や分かったことについてメモを残しておきます。

## 結論だけ知りたい人向け

**`(*Client).UploadFileContext`を使っていた処理を`(*Client).UploadFileV2Context`に書き換えると良いみたいです。パラメータはいくつか変わるものの、基本的にはほぼそのままで利用可能です。**
筆者はローカルのファイルをアップロードする使い方をしていたので、`*os.File`型を引数にとる使い方をしていたので大きな影響はなかったですが、`io.Reader`型を引数に取る使い方をしていた方はアップロードするファイルのサイズをどこかで知る必要があります。


こちらの記事にもわかりやすくまとまっています。

https://zenn.dev/ikawaha/articles/20240505-842774e0b280d4

いちおうコードも載せておきます。

こちらが今まで動いていた書き方

```go:asis.go
func UploadFile_AsIs(ctx context.Context, slackToken string, chID string, file *os.File) (*slack.File, error) {
	api := slack.New(slackToken)

	// ファイルをアップロード
	f, err := api.UploadFileContext(
		ctx,
		slack.FileUploadParameters{
			Reader:   file,
			Filename: "upload file name",
			Channels: []string{chID},
			Title:    "upload file title",
		})
	return f, err
}
```

こちらが新しい書き方

```go:tobe.go
func UploadFile_ToBe(ctx context.Context, slackToken string, chID string, file *os.File) (*slack.FileSummary, error) {
	api := slack.New(slackToken)

	// アップロードするファイルのサイズを取得
	fileStat, err := file.Stat()
	if err != nil {
		panic(err)
	}
	size := fileStat.Size()

	// ファイルをアップロード
	f, err := api.UploadFileV2Context(ctx, slack.UploadFileV2Parameters{
		FileSize: int(size),
		Reader:   file,
		Filename: "upload file name",
		Title:    "upload file title",
		Channel:  chID,
	})
	return f, err
}
```

作成したサンプルコードの全量はこちらに置いておきます

https://github.com/katonium/articles/blob/zenn/main/samplecodes/slack-file-upload/main.go


## ...これだと便利すぎてなにが変わったのか全然わからない！

ライブラリの設計として、外部から呼び出すインターフェースがほとんど変わらないまま破壊的な変更を吸収しているこの実装は素晴らしいなと感じました。

...が、いち開発者としては中身が気になる部分もあるので、少しだけソースコードを追いかけてみます。
まずは[(*Client) UploadFileV2Context](https://github.com/slack-go/slack/blob/v0.14.0/files.go#L576-L619)のあたりから

:::details (*Client) UploadFileV2Contextのソースコード
```go:slack/files.go
func (api *Client) UploadFileV2Context(ctx context.Context, params UploadFileV2Parameters) (file *FileSummary, err error) {
    // 0. バリデーションチェック
	if params.Filename == "" {
		return nil, fmt.Errorf("file.upload.v2: filename cannot be empty")
	}
	if params.FileSize == 0 {
		return nil, fmt.Errorf("file.upload.v2: file size cannot be 0")
	}

    // 1. files.getUploadURLExternalをコールしファイルをアップロードする先のURLを取得
    u, err := api.getUploadURLExternal(ctx, getUploadURLExternalParameters{
		altText:     params.AltTxt,
		fileName:    params.Filename,
		fileSize:    params.FileSize,
		snippetText: params.SnippetText,
	})
	if err != nil {
		return nil, err
	}

    // 2. 1で取得したURLにファイルをアップロード
	err = api.uploadToURL(ctx, uploadToURLParameters{
		UploadURL: u.UploadURL,
		Reader:    params.Reader,
		File:      params.File,
		Content:   params.Content,
		Filename:  params.Filename,
	})
	if err != nil {
		return nil, err
	}

    // 3. files.completeUploadExternalをコールしアップロードが完了したファイルをSlackに投稿
	c, err := api.completeUploadExternal(ctx, u.FileID, completeUploadExternalParameters{
		title:           params.Title,
		channel:         params.Channel,
		initialComment:  params.InitialComment,
		threadTimestamp: params.ThreadTimestamp,
	})
	if err != nil {
		return nil, err
	}
	if len(c.Files) != 1 {
		return nil, fmt.Errorf("file.upload.v2: something went wrong; received %d files instead of 1", len(c.Files))
	}

	return &c.Files[0], nil
}
```
:::

バリデーションチェックを除くと3つの処理を実施していることが分かります。

1. files.getUploadURLExternalをコールしファイルをアップロードする先のURLを取得
2. 1で取得したURLにファイルをアップロード
3. files.completeUploadExternalをコールしアップロードが完了したファイルをSlackに投稿

これは[Slack公式が出している移行方法](https://api.slack.com/changelog/2024-04-a-better-way-to-upload-files-is-here-to-stay)の中身と同じですね。


また、uploadToURL関数の中ではパラメータに応じてアップロード処理を切り替えているようですね、**綺麗に書いてありすぎて正直脱帽です**。**`(*Client) UploadFileV2Context`呼び出し時の`UploadFileV2Parameters`引数にて`Content`ではなく`File`を指定した場合にはローカルからファイルを読みこんで送信してくれる**ようで、これはかなり使いやすそう。


:::details uploadToURLのソースコード
```go:slack/files.go
func (api *Client) uploadToURL(ctx context.Context, params uploadToURLParameters) (err error) {
	values := url.Values{}
	if params.Content != "" {
		contentReader := strings.NewReader(params.Content)
		err = postWithMultipartResponse(ctx, api.httpclient, params.UploadURL, params.Filename, "file", api.token, values, contentReader, nil, api)
	} else if params.File != "" {
		err = postLocalWithMultipartResponse(ctx, api.httpclient, params.UploadURL, params.File, "file", api.token, values, nil, api)
	} else if params.Reader != nil {
		err = postWithMultipartResponse(ctx, api.httpclient, params.UploadURL, params.Filename, "file", api.token, values, params.Reader, nil, api)
	}
	return err
}
```
:::

## 考察: なぜこれらのAPI呼び出しは非公開関数として作成されているのか？

さて、`(*Client) UploadFileV2Context`関数内で利用されていた下記3ステップの関数はそれぞれ非公開メソッドとして記述されています。

> 1. files.getUploadURLExternalをコールしファイルをアップロードする先のURLを取得
> 2. 1で取得したURLにファイルをアップロード
> 3. files.completeUploadExternalをコールしアップロードが完了したファイルをSlackに投稿

ステップ2はファイルをアップロードする機能だったこともあり非公開にすることに特に違和感はないですが、1と3はSlackが公開しているAPIを叩いているので、公開関数にしても良いのではないかと感じました。


`(*Client) UploadFileV2Context`は[PR #1130: Added new FileUploadV2 function to avoid server side file timeouts](https://github.com/slack-go/slack/pull/1130)というPull Requestにて追加されたようです。
この変更は[Issue #1108: Large File upload causes timeout](https://github.com/slack-go/slack/issues/1108)に関連する修正とPull Requestを出した方が書いている通り、そもそもfile upload V2に関係するAPIはもともとSlackへの大容量ファイルアップロードが失敗する問題が(Go SDKに限らず)あってSlack側が追加したAPIだったようですね。

Python側のSDKにも同様のリリースノートが展開されており、既存ライブラリのラッパーとしてv2版の関数を公開するぜ、って書いてあるのでこの思想がGo側のライブラリにも入った結果の非公開関数だったように読み取れます。

[python-slack-sdk version 3.19.0のリリースノート](https://github.com/slackapi/python-slack-sdk/releases/tag/v3.19.0)

https://github.com/slackapi/python-slack-sdk/releases/tag/v3.19.0

Slack側もおそらくセットでこれらのAPIが使われることを想定しているのかなと思います。

それをくみ取って非公開関数にすることでセットで利用するのを強制する、と想像するのは考えすぎかもしれませんが①ユーザーの使いやすさや②関数が増えることによるライブラリの複雑度増加の観点から考えると非公開にしたのは適切だったのかなと感じます。勉強になります。


## まとめ

途中脱線しましたが、3つのことが分かりました！

1. `slack-go/slack`は`(*Client).UploadFileContext`を`(*Client).UploadFileV2Context`に書き換えるだけで新しいAPIに移行できる
2. `(*Client).UploadFileV2Context`においてはファイルを引数に取ってあげるとファイルの読み込みからやってくれて便利 (v1からあった機能かもですが)
3. Golangのライブラリを書く際には公開・非公開のキリとしてユーザーがどう使うかを想定してあげると使いやすいライブラリになる (もちろん他にもいろんな思想がありますが、1つの指標として)

よいSlack・よいGoライフを！
