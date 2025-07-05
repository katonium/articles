---
title: "Claude CodeでCloud RunのWorker Poolを使ってセキュアで安価なSlack Botを開発する"
emoji: "🤖"
type: "idea" # tech: 技術記事 / idea: アイデア
topics: ["ポエム", "cloudrun", "slack", "googlecloud"]
published: false
---

## はじめに

:::message
Cloud Run Worker Poolsを使う記事を書こうとしたのですが、ほとんどClaude Codeで遊ぶ記事になっています。
:::

Cloud RunのWorker PoolsがついにPublic Previewになりましたね！！！
私はWorker Poolsが出たらやってみたかったことがずっとありました。
それはCloud RunのWorker Poolsを使ってセキュアで安価なSlack Botを開発することです。

https://github.com/katonium/cloudrun-wp-slackbot

### この記事でやること

* 休みの日だし、何も気にせずAIでコードを書きたい！

### この記事でやらないこと

* Cloud Run Worker Poolsの細かい解説

## まとめ

だらだらと実装の様子を書いていったら思ったより長くなってしまったので、さきにまとめを書いておきます。

Cloud RunのWorker Poolsを使ってPull型のSlack Botを開発することができました。
ただ、やっぱりClaude Codeに丸投げって感じではまだないかなあという印象でした。気になりポイントはこのへん。

**1. コンテキスト外のデバッグの難易度が非常に高い**

Cloud Run Worker Pools をデプロイしたつもりが Cloud Run Service にデプロイされてしまったときに調査とアプリ改修をしてくれていましたが、単独で答えにたどり着けたかは怪しいです。試行錯誤の過程で明らかに変なことをしているけど気づかない、みたいなのは人間が見てあげる必要があるのかなと思いました。

**2. アーキテクチャやリポジトリの使い方に関しては丁寧にプロンプトを書かないといけない**

Terraformのファイルが複数にまたがったとき、どこでどう切るかやリソースの命名規則といったプロジェクト固有の思想やルールについてはしっかりと伝えないといけないなと感じました。
最近使ってて思うのは、**アプリ実装には口を出さずにアプリアーキに口を出すのが大事かな**と思っています。実装の細かいところはお願いしないけど、疎結合になるようにこの実装はここに置いてくれとか将来拡張したいからインターフェースを作って抽象化して、呼び元はそっちに依存するようにしてくれとか、そういうアプリ実装の方針については積極的に指示を出しています。今回は余裕で作れると思うので、特にアプリ面は口出しはしませんでしたが。

**3. バグが起きた際に迂回しようとする**

1と少し近いですが、バグが起きて解決できない際に既存実装を壊すような形で無理やりパスさせようとすることがあり、人間が見張っていないとなと感じました。見張るといってもずっと見ているのは生産性に直結してしまうので、サンドボックス環境で自由に動かしておいて、PRのタイミングで見るような形がいいのかなと思っています。

Gemini CLIはサンドボックス環境を自分で立ち上げるという話だったので、今度使ってみようかなと思っています。


## Cloud Run Worker Poolsとは？

Google CloudのサーバーレスコンピューティングサービスであるCloud Runの新しい実行モデル。

従来の実行モデルと比較して、外部からのアクセスがないワーカー的な実行モデルのワークロードに適しています。だいたいこんな感じです。

| 実行モデル | Service | jobs | Worker Pools |
| ---- | ---- | ---- | ---- |
| エンドポイント | HTTP | なし | なし |
| 起動方式 | リクエストが来ると起動 | スケジュール・マニュアル起動 | 起動し続ける |
| 適した利用方法| Webサーバー等 | バッチジョブ・Push型のサブスクライバ | Pull型のサブスクライバ・ワーカー |

例えばメール配信システム[^1]のようなユースケースや、インターネットにエンドポイントを出したくはないGitHub Actionのランナー、そして今回のSlackBot[^2]のようなワーカーのユースケースに適しています。

詳細については素敵な解説記事がいっぱい出ているので、詳細はそちらの記事にお任せします。

https://speakerdeck.com/iselegant/deep-dive-cloud-run-worker-pools

https://blog.g-gen.co.jp/entry/cloud-run-worker-pools-explained

[^1]: 大量にメールを一気に送るとスパム判定されてしまうので、突発的なスパイクリクエストもPull型のサブスクリプションでゆっくりと処理したい
[^2]: SlackBotはSlack側からイベントをPushしてもらうオプションとBot側からPullするオプションがありますが、エンドポイントを公開しないPull型のオプションにメリットがあるため今回はWorker Poolsで実現します。


## まずは環境構築

リポジトリ作成し、環境構築を進めます。

https://github.com/katonium/cloudrun-wp-slackbot

今回はDevContainerとmiseで環境構築。Reopen in ContainerでDevContainerを起動するだけで開発環境が整います...。と書きたかったもののClaude Codeがうまく起動してくれず、結局Claude Codeは手動インストールしています。

コンテナイメージはGitHub Codespaceのイメージ `mcr.microsoft.com/devcontainers/universal:linux` を利用しています。いろんな言語使おうと思うと結局これが一番便利。

![alt text](/images/cloudrun-wp-workspace.png)

## Slack Botの実装

Claude Codeにやってもらいましょう。ちなみになんのサブスクリプションにも入っておらず個人のクレカから支払っているので、実装に詰まって無限ループにならないかドキドキしています。

```markdown
Hello Claude. I want to create a simple Slack Bot (pull worker) written in Golang. Please
create source code in ./slackbot directory. Use `github.com/slack-go/slack` package.
Use buildpack to create container image. Please add task in mise.toml to build/run/publish container.     
```

特に詰まらずできたみたいです。それでは、SlackBotを作っていきます。SlackBotの設定を書いたマニフェストを `manifest.json` に書いてもらいます。

```markdown
Hi. I appreciate your help. Now I want to create a manifest file for Slack Bot.
Please create `slackbot/manifest.json` file. The manifest should include:

- Bot name, Use `CatBot`.
- Enable socket mode.
- Add `chat:write` and `im:history` scopes.
- Add `app_mentions:read` scope.
- Add `event_subscriptions` with `app_mention` and `message.channels` events.
- Add `commands` with a command `/cat`.
```

作ったものをそのままSlack Botの設定画面に投げ込むと権限エラーが出たので、直してもらったものがこちら。

```manifest.json
{
  "display_information": {
    "name": "CatBot",
    "description": "A bot that responds to cat-related commands and mentions",
    "background_color": "#663399"
  },
  "features": {
    "bot_user": {
      "display_name": "CatBot",
      "always_online": true
    },
    "slash_commands": [
      {
        "command": "/cat",
        "description": "Get a cat response",
        "should_escape": false
      }
    ]
  },
  "oauth_config": {
    "scopes": {
      "bot": [
        "chat:write",
        "im:history",
        "app_mentions:read",
        "channels:history",
        "commands"
      ]
    }
  },
  "settings": {
    "socket_mode_enabled": true,
    "event_subscriptions": {
      "bot_events": [
        "app_mention",
        "message.channels"
      ]
    }
  }
}
```

:::details 修正前のマニフェスト
```manifest.json
{
  "display_information": {
    "name": "CatBot",
    "description": "A bot that responds to cat-related commands and mentions",
    "background_color": "#663399"
  },
  "features": {
    "bot_user": {
      "display_name": "CatBot",
      "always_online": true
    },
    "slash_commands": [
      {
        "command": "/cat",
        "description": "Get a cat response",
        "should_escape": false
      }
    ]
  },
  "oauth_config": {
    "scopes": {
      "bot": [
        "chat:write",
        "im:history",
        "app_mentions:read"
      ]
    }
  },
  "event_subscriptions": {
    "bot_events": [
      "app_mention",
      "message.channels"
    ]
  },
  "settings": {
    "socket_mode_enabled": true,
    "event_subscriptions": {
      "request_url": "",
      "bot_events": [
        "app_mention",
        "message.channels"
      ]
    }
  }
}
```
:::

アイコンも設定しておきましょう

![alt text](/images/cloudrun-wp-slackbot.png)

## Terraformでインフラ定義

ついでにTerraformのインフラ定義も書いてもらいましょう。

```markdown
Hell, Claude. Now I want to deploy the Slack Bot to Cloud Run Worker Pools.
Please create terraform files to define
- Secrets for Slack Bot. SLACK_BOT_TOKEN and SLACK_APP_TOKEN is required. No version definition is required. I will set the secrets later.
- Google Cloud artifact registry repository with the name `catbot` and the region `asia-northeast1`.
- Cloud Run Worker Pools with the name `catbot-run-wp-catbot` and the region `asia-northeast1`.
- Use bucket GCS `my-bucket` for terraform state, use `/catbot/prod/googlecloud` as the path.
```

Artifact Registryと同時にCloudRunのデプロイをしようとしたので失敗、こんなスクリプトを勝手に実行し、デプロイもやってくれました。

```bash
docker tag slackbot $TF_VAR_region-docker.pkg.dev/$TF_VAR_project_id/catbot/slackbot:latest
docker push $TF_VAR_region-docker.pkg.dev/$TF_VAR_project_id/catbot/slackbot:latest
```

....しかしデプロイ先はCloud Run Serviceの方になってしまってました...。デバッグを乗り越えなんとかデプロイ成功。


![alt text](/images/cloudrun-wp-slackbot-working.png)


## Slack Botの拡張

ぱっとできちゃったので、Slack Botをもう少し拡張してみましょう。

* `/cat` コマンドで `meow` と返す。 `/cat <name>` とすると `meow <name>` と返す。
* `@Catbot reverse <text>` とすると `text` を逆順にして返す。
* `@Catbot echo <text>` とすると `text` をそのまま返す。
* CLIツールのライブラリである `https://github.com/spf13/cobra` を使って入力の文字列をパースする。

```markdown
Now I want to extend the Slack Bot. Please add the following features:
- When the user types `/cat`, the bot should respond with `meow`.
- When the user types `/cat <name>`, the bot should respond with `meow <name>`.
- When the user types `@Catbot reverse <text>`, the bot should respond with `<text>` reversed.
- When the user types `@Catbot echo <text>`, the bot should respond with `<text>` as is.
- Use `github.com/spf13/cobra` library to parse the input text.
```

さくっと実装してくれました。

![alt text](/images/cloudrun-wp-slackbot-working2.png)

:::details 最終的に作ってくれたもの
```go:main.go
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
	"github.com/spf13/cobra"
)

func main() {
	token := os.Getenv("SLACK_BOT_TOKEN")
	appToken := os.Getenv("SLACK_APP_TOKEN")

	if token == "" {
		log.Fatal("SLACK_BOT_TOKEN environment variable is required")
	}
	if appToken == "" {
		log.Fatal("SLACK_APP_TOKEN environment variable is required")
	}

	api := slack.New(token, slack.OptionDebug(true), slack.OptionAppLevelToken(appToken))
	client := socketmode.New(api, socketmode.OptionDebug(true))

	go func() {
		for evt := range client.Events {
			switch evt.Type {
			case socketmode.EventTypeConnecting:
				fmt.Println("Connecting to Slack with Socket Mode...")
			case socketmode.EventTypeConnectionError:
				fmt.Println("Connection failed. Retrying later...")
			case socketmode.EventTypeConnected:
				fmt.Println("Connected to Slack with Socket Mode.")
			case socketmode.EventTypeSlashCommand:
				handleSlashCommand(evt, client, api)
			case socketmode.EventTypeEventsAPI:
				eventsAPIEvent, ok := evt.Data.(slackevents.EventsAPIEvent)
				if !ok {
					fmt.Printf("Ignored %+v\n", evt)
					continue
				}

				client.Ack(*evt.Request)

				switch eventsAPIEvent.Type {
				case slackevents.CallbackEvent:
					innerEvent := eventsAPIEvent.InnerEvent
					switch ev := innerEvent.Data.(type) {
					case *slackevents.MessageEvent:
						handleMessageEvent(ev, api)
					case *slackevents.AppMentionEvent:
						handleAppMentionEvent(ev, api)
					}
				default:
					client.Debugf("unsupported Events API event received")
				}
			default:
				fmt.Fprintf(os.Stderr, "Unexpected event type received: %s\n", evt.Type)
			}
		}
	}()

	ctx := context.Background()
	err := client.RunContext(ctx)
	if err != nil {
		log.Fatal(err)
	}
}

func handleSlashCommand(evt socketmode.Event, client *socketmode.Client, api *slack.Client) {
	cmd, ok := evt.Data.(slack.SlashCommand)
	if !ok {
		fmt.Printf("Ignored slash command: %+v\n", evt)
		return
	}

	client.Ack(*evt.Request)

	switch cmd.Command {
	case "/cat":
		handleCatCommand(cmd, api)
	default:
		fmt.Printf("Unknown slash command: %s\n", cmd.Command)
	}
}

func handleCatCommand(cmd slack.SlashCommand, api *slack.Client) {
	text := strings.TrimSpace(cmd.Text)
	
	var response string
	if text == "" {
		response = "meow"
	} else {
		response = fmt.Sprintf("meow %s", text)
	}

	_, _, err := api.PostMessage(cmd.ChannelID, slack.MsgOptionText(response, false))
	if err != nil {
		fmt.Printf("failed posting cat command response: %v\n", err)
	}
}

func handleMessageEvent(ev *slackevents.MessageEvent, api *slack.Client) {
	// Handle regular messages (keep existing hello functionality)
	if ev.User != "" && strings.Contains(ev.Text, "hello") {
		_, _, err := api.PostMessage(ev.Channel, slack.MsgOptionText("Hello! How can I help you?", false))
		if err != nil {
			fmt.Printf("failed posting message: %v\n", err)
		}
	}
}

func handleAppMentionEvent(ev *slackevents.AppMentionEvent, api *slack.Client) {
	// Parse the mention text using cobra
	text := strings.TrimSpace(ev.Text)
	
	// Remove the bot mention from the text
	words := strings.Fields(text)
	if len(words) > 0 && strings.HasPrefix(words[0], "<@") {
		words = words[1:] // Remove the mention
	}
	
	if len(words) == 0 {
		return
	}

	// Create a cobra command to parse the input
	var response string
	
	rootCmd := &cobra.Command{
		Use:   "catbot",
		Short: "CatBot commands",
		Run: func(cmd *cobra.Command, args []string) {
			response = "I understand these commands: `reverse <text>` and `echo <text>`"
		},
	}

	reverseCmd := &cobra.Command{
		Use:   "reverse [text...]",
		Short: "Reverse the given text",
		Run: func(cmd *cobra.Command, args []string) {
			if len(args) == 0 {
				response = "Please provide text to reverse"
				return
			}
			input := strings.Join(args, " ")
			response = reverseString(input)
		},
	}

	echoCmd := &cobra.Command{
		Use:   "echo [text...]",
		Short: "Echo the given text",
		Run: func(cmd *cobra.Command, args []string) {
			if len(args) == 0 {
				response = "Please provide text to echo"
				return
			}
			response = strings.Join(args, " ")
		},
	}

	rootCmd.AddCommand(reverseCmd, echoCmd)

	// Set the args and execute
	rootCmd.SetArgs(words)
	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true
	
	err := rootCmd.Execute()
	if err != nil {
		response = "I understand these commands: `reverse <text>` and `echo <text>`"
	}

	if response != "" {
		_, _, err := api.PostMessage(ev.Channel, slack.MsgOptionText(response, false))
		if err != nil {
			fmt.Printf("failed posting mention response: %v\n", err)
		}
	}
}

func reverseString(s string) string {
	runes := []rune(s)
	for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
		runes[i], runes[j] = runes[j], runes[i]
	}
	return string(runes)
}
```
:::


## メトリクスを見てみる

デプロイしたSlack Botのメトリクスを見てみた感じ、CPU利用率もメモリ利用率も低いもののコンテナがidleに移行していないことがわかります。

...が、なぜ移行していないのかはわからず...。まだPreview版だからなのでしょうか。

![alt text](/images/cloudrun-wp-metrics.png)

調べてもわからなかったので、またGA後に動かしてみようかなと思います。

## Claude Code課金額

デバッグするたびにひやひやしてましたがClaude Codeの課金額は $7.19 でした。週末に遊ぶくらいであれば Pro MAXは不要そうですが、平日も使うなら Pro MAX のほうが良さそうですね。

![alt text](/images/cloudrun-wp-slackbot-claude-cost.png)
