package main

import (
    "context"
    "fmt"
    "log"

    "github.com/ServiceWeaver/weaver"
)

func main() {
    if err := weaver.Run(context.Background(), serve); err != nil {
        log.Fatal(err)
    }
}

// app - メインとなるコンポーネントの定義です。ServiceWeaverはこのコンポーネント単位に物理プロセスを割り当てます。
type app struct{
    weaver.Implements[weaver.Main]
}

// serveはweaver.Runによって呼び出されるメイン処理です。引数となるapp構造体はServiceWeaberによって注入されます。
func serve(context.Context, *app) error {
    fmt.Println("Hello")
    return nil
}