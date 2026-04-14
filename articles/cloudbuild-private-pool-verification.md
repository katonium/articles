---
title: "Cloud Build プライベートプールの仕様をTerraform + Go Testで検証する"
emoji: "🔒"
type: "tech"
topics: ["googlecloud", "cloudbuild", "terraform", "go", "vpc"]
published: false
---

## はじめに

Cloud Buildのプライベートプールを使うと、Google Cloudマネージドなビルドワーカーを自前のVPC内に配置できます。VMの管理は不要でありながら、VPCのネットワーク制御やサービスアカウントの使い分けが可能です。

本記事では、以下の仕様をTerraform + Goのテストパッケージで宣言的に検証します。

### 検証項目

1. **VPC統合**: マネージドWorkerが自前VPC内に配置され、VPCのネットワーク制御に従うこと
   - Private Google Access経由でGCS・Artifact Registryにアクセスできること
   - VPCファイアウォールルールによって外部通信を制御できること
2. **サービスアカウント分離**: 単一のワーカープールで、ビルドジョブごとにSAを切り替えられること
   - 異なるSAで実行した2つのジョブが、それぞれ異なるバケットにのみアクセスできること

:::message
検証コードは以下のリポジトリにあります。
https://github.com/katonium/articles/tree/worktree-cloudbuild-private-pool/samplecodes/cloudbuild-private-pool
:::

## プライベートプールのアーキテクチャ

### VPC Peering によるネットワーク統合

プライベートプールのワーカーは**Googleが所有するサービスプロデューサーネットワーク**内で動作し、ユーザーのVPCとは**Private Services Access（Service Networking API）経由のVPCピアリング**で接続されます。

```
┌─────────────────────────┐     VPC Peering     ┌──────────────────────────┐
│   ユーザーのVPC          │◄──────────────────►│  Google マネージドVPC     │
│                         │  (Service Networking) │                          │
│  ┌─────────┐ ┌───────┐ │                      │  ┌────────────────────┐  │
│  │  GCS    │ │  AR   │ │                      │  │  Private Pool      │  │
│  │ Bucket  │ │ Repo  │ │                      │  │  Worker VMs        │  │
│  └─────────┘ └───────┘ │                      │  │  (Google管理)       │  │
│                         │                      │  └────────────────────┘  │
│  FW Rules / Routes      │                      │                          │
│  Cloud NAT              │                      │                          │
└─────────────────────────┘                      └──────────────────────────┘
```

重要なポイント:

- ワーカーVMの管理（プロビジョニング、パッチ適用等）はGoogleが行う
- ユーザーVPCのルーティング・NAT・ファイアウォールルールを経由して通信する
- `networkConfig`を省略するとピアリングなしで動作するが、VPC内リソースには到達不可
- **トランジティブピアリングは非対応**: VPCに別途ピアリングされたCloud SQL等には到達できない（VPN/Interconnectが必要）

https://cloud.google.com/build/docs/private-pools/private-pools-overview

https://cloud.google.com/build/docs/private-pools/set-up-private-pool-to-use-in-vpc-network

### networkConfig のオプション

| フィールド | 必須 | 説明 |
|---|---|---|
| `peeredNetwork` | Yes（networkConfig指定時） | ピアリング先のVPCネットワークリソースURL |
| `peeredNetworkIpRange` | No | ワーカー用IPレンジ（CIDR、prefix ≤ /29、デフォルト /24） |
| `egressOption` | No | 省略=`PUBLIC_EGRESS`（外部通信可）、`NO_PUBLIC_EGRESS`（外部通信不可） |

:::message alert
`192.168.10.0/24` と `172.17.0.0/16` はDockerブリッジネットワークに予約されているため使用不可。
:::

https://cloud.google.com/build/docs/private-pools/private-pool-config-file-schema

## 本番運用での考慮事項

### VPC Service Controls との組み合わせ

プライベートプールをVPC Service Controlsのペリメータ内で使う場合:

- プール所属プロジェクトをペリメータに追加し、`egressOption: NO_PUBLIC_EGRESS` を設定する必要がある
- **レガシーCloud Build SA** (`PROJECT_NUMBER@cloudbuild.gserviceaccount.com`) はデフォルトでペリメータ外扱い → Ingressルールの追加が必要
- ユーザー指定SAをAPI/CLI経由で使う場合はレガシーSAのIngressルール不要
- `NO_PUBLIC_EGRESS` ではインターネット（GitHub, Docker Hub等）に到達不可
- Pub/SubトリガーはVPC-SC非対応

https://cloud.google.com/build/docs/private-pools/using-vpc-service-controls

### Shared VPC パターン

Shared VPC環境でプライベートプールを使う場合、**ピアリング接続はホストプロジェクトのVPCに対して1回作成すれば全サービスプロジェクトで共有される**ため、ゲストプロジェクトごとにピアリングが増殖する問題は発生しません。

- IPレンジ割り当て・プライベート接続はホストプロジェクト側で作成
- サービスプロジェクトがホストプロジェクトにアタッチ済みであること
- ホスト・サービスプロジェクトは同一組織内であること（Shared VPC自体が組織必須）
- VPC-SC使用時は同一ペリメータ内であること

https://cloud.google.com/build/docs/private-pools/set-up-private-pool-to-use-in-vpc-network

https://cloud.google.com/vpc/docs/private-services-access

## 検証環境の構成

### Terraform で構築するリソース

| リソース | 用途 |
|---|---|
| VPC + Subnet | プライベートプール配置先 |
| Cloud Router + Cloud NAT | ワーカーの外部通信用 |
| Firewall Rules | deny all egress → 特定IPのみallow |
| Service Networking Connection | Google マネージドVPCとのピアリング |
| Cloud Build Worker Pool | 検証対象のプライベートプール |
| Service Account x2 (SA-A, SA-B) | ジョブごとのSA切り替え検証用 |
| GCS Bucket x2 (Bucket-A, Bucket-B) | SA別アクセス制御の証明用 |
| Artifact Registry Repository | VPC内からの接続確認用 |

すべてのリソース名には `random_id` によるランダムサフィックスを付与し、再現検証時の名前衝突を防止しています。

### ファイアウォールルール設計

VPCファイアウォールで「許可されたIPのみ通信可能」な状態を作り、ワーカーがVPCのネットワーク制御に従うことを証明します。

| ルール | 優先度 | 方向 | 宛先 | アクション |
|---|---|---|---|---|
| deny-all-egress | 65534 | EGRESS | 0.0.0.0/0 | DENY |
| allow-google-apis | 1000 | EGRESS | 199.36.153.4/30 | ALLOW (tcp:443) |
| allow-test-ip | 1000 | EGRESS | 8.8.8.8/32 | ALLOW (tcp:443,53 udp:53) |
| allow-internal | 1000 | EGRESS | 10.0.0.0/8 等 | ALLOW (all) |

## テストケース

### Test 1: VPC統合検証 (`TestPrivatePool_VPCIntegration`)

| # | テスト名 | ビルドステップ | 期待結果 |
|---|---|---|---|
| 1-1a | GCSにVPC内部からアクセスできること | `gsutil cp` でBucket-Aに書き込み | SUCCESS |
| 1-1b | Artifact RegistryにVPC内部から接続できること | `gcloud artifacts docker images list` | SUCCESS |
| 1-2a | FWで許可されたIP (8.8.8.8) にアクセスできること | `curl https://dns.google` | SUCCESS |
| 1-2b | FWで許可されていないIP (1.1.1.1) にアクセスできないこと | `curl https://1.1.1.1` | FAILURE (タイムアウト) |

### Test 2: サービスアカウント分離検証 (`TestPrivatePool_ServiceAccountIsolation`)

| # | テスト名 | 実行SA | 対象バケット | 期待結果 |
|---|---|---|---|---|
| 2-1a | SA-AはBucket-Aにアクセスできること | SA-A | Bucket-A | SUCCESS |
| 2-1b | SA-AはBucket-Bにアクセスできないこと | SA-A | Bucket-B | FAILURE (403) |
| 2-1c | SA-BはBucket-Bにアクセスできること | SA-B | Bucket-B | SUCCESS |
| 2-1d | SA-BはBucket-Aにアクセスできないこと | SA-B | Bucket-A | FAILURE (403) |

## テスト実行結果

### VPC統合検証

```
=== RUN   TestPrivatePool_VPCIntegration
=== RUN   TestPrivatePool_VPCIntegration/正常系_GCSにVPC内部からアクセスできること
    ビルド ID: 8c0d675b-d3ad-4e34-8692-5a9915a4ba7c
--- PASS (92.15s)
=== RUN   TestPrivatePool_VPCIntegration/正常系_ArtifactRegistryにVPC内部から接続できること
    ビルド ID: 1f6ef195-d41e-4b4b-be39-f0fdd6ab76f1
--- PASS (89.81s)
=== RUN   TestPrivatePool_VPCIntegration/正常系_FWで許可されたIP_8.8.8.8_にアクセスできること
    ビルド ID: ddc5c869-2d5a-4a7f-b7ad-e85dac74f4cb
--- PASS (63.28s)
=== RUN   TestPrivatePool_VPCIntegration/異常系_FWで許可されていないIP_1.1.1.1_にアクセスできないこと
    ビルド ID: 8cc78a18-aa0f-4568-941c-8fcc49f16953
    期待通りビルドが失敗: ステータス=FAILURE
--- PASS (27.52s)
--- PASS: TestPrivatePool_VPCIntegration (272.86s)
```

全テストが期待通りの結果となりました。

- **GCS / Artifact Registry**: プライベートプール内のワーカーから Private Google Access 経由で正常にアクセスできることを確認
- **ファイアウォール制御**: `8.8.8.8`（FWで許可）への通信は成功し、`1.1.1.1`（FWで未許可）への通信はビルドが失敗 → **ワーカーがVPCのファイアウォールルールに従っている**ことが証明された

### SA分離検証

```
=== RUN   TestPrivatePool_ServiceAccountIsolation
=== RUN   TestPrivatePool_ServiceAccountIsolation/正常系_SA-AはBucket-Aにアクセスできること
    ビルド ID: d9fa43ae-d739-4d2e-99ac-c067d72a6a54
--- PASS (87.38s)
=== RUN   TestPrivatePool_ServiceAccountIsolation/異常系_SA-AはBucket-Bにアクセスできないこと
    ビルド ID: c3f683fb-eb43-49c5-879c-ee812798c102
    期待通り SA-A は Bucket-B にアクセス拒否: ステータス=FAILURE
--- PASS (63.80s)
=== RUN   TestPrivatePool_ServiceAccountIsolation/正常系_SA-BはBucket-Bにアクセスできること
    ビルド ID: 339290c2-d949-4958-8724-cb41b320ff02
--- PASS (71.68s)
=== RUN   TestPrivatePool_ServiceAccountIsolation/異常系_SA-BはBucket-Aにアクセスできないこと
    ビルド ID: ab1bef19-723e-427c-a416-1727938283f2
    期待通り SA-B は Bucket-A にアクセス拒否: ステータス=FAILURE
--- PASS (66.35s)
--- PASS: TestPrivatePool_ServiceAccountIsolation (289.36s)
```

- **SA-A**: Bucket-Aへの書き込みは成功、Bucket-Bへの書き込みは権限エラーで失敗
- **SA-B**: Bucket-Bへの書き込みは成功、Bucket-Aへの書き込みは権限エラーで失敗

**単一のプライベートプールであっても、ビルドジョブごとにサービスアカウントを切り替えることで、アクセス可能なリソースを制御できる**ことが証明されました。

### 実装上のハマりどころ

テスト実装中に遭遇した注意点を記載しておきます。

#### カスタムSA使用時のログ設定が必須

`build.service_account` にカスタムSAを指定する場合、以下のいずれかを設定する必要があります:

1. `build.logs_bucket` でログ保存先GCSバケットを指定
2. `build.options.default_logs_bucket_behavior` に `REGIONAL_USER_OWNED_BUCKET` を設定
3. `build.options.logging` に `CLOUD_LOGGING_ONLY` または `NONE` を設定

今回は `CLOUD_LOGGING_ONLY` を使用しました。これを設定しないと `InvalidArgument` エラーが返ります。

#### リージョナルAPIエンドポイントの指定

プライベートプールはリージョナルリソースであるため、Cloud Build APIのクライアント作成時にリージョナルエンドポイントを指定する必要があります。

```go
endpoint := fmt.Sprintf("%s-cloudbuild.googleapis.com:443", region)
client, err := cloudbuild.NewClient(ctx, option.WithEndpoint(endpoint))
```

デフォルトのグローバルエンドポイント（`cloudbuild.googleapis.com`）を使用すると、プライベートプールが `NotFound` になります。

## まとめ

Cloud Buildのプライベートプールについて、以下の仕様を実際のテストで確認しました:

| 検証項目 | 結果 |
|---|---|
| マネージドWorkerがVPC内に配置される（VM管理不要） | **確認済み** |
| GCS / Artifact RegistryにPrivate Google Access経由でアクセス可能 | **確認済み** |
| VPCファイアウォールルールによる外部通信制御が有効 | **確認済み** |
| 単一プールで複数SAの切り替えが可能 | **確認済み** |
| SAごとにアクセス可能なリソースが適切に制限される | **確認済み** |

プライベートプールは「VMの管理なしにVPCのネットワーク制御を適用できるマネージドビルド環境」として、期待通りに機能することが確認できました。本番運用ではVPC Service ControlsやShared VPCとの組み合わせも考慮が必要ですが、基本的なネットワーク分離とSA分離は十分に機能します。
