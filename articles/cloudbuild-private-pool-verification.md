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

:::message
テスト実行後に結果を記載予定
:::

## まとめ

:::message
テスト結果に基づいてまとめを記載予定
:::
