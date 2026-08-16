# VPC-SC 越し Cloud Build Private Pool 検証

## 検証の意図

VPC Service Controls 配下に Cloud Build Private Pool を置くと、ある条件下では
追加の Ingress / Egress ポリシーなしで境界内のリソース（Artifact Registry 等）へ
アクセスできる。これは「Private Pool が **暗黙に** VPC-SC 境界の内側として扱われる」
という挙動による。

ただし、どのリソース構成のときに「境界内」と判定され、どの構成だと「境界外」と
扱われるのかが、Cloud Build / VPC-SC / Shared VPC の組み合わせによって変わる。

本検証では以下の問いに、Terraform で構成を作り Go テストで build job を回して
答えることを目的とする:

- どの条件で Private Pool は VPC-SC 境界内として振る舞うのか
- どの条件だと境界外扱いになり拒否されるのか
- Shared VPC（ホスト / ゲスト分離）の場合に挙動はどう変わるか
- 境界が host / guest で分かれているとき、どちらの project にだけ通るのか

## 検証の構成

### インフラ全体図

```mermaid
flowchart TB
  subgraph Org["Organization"]
    subgraph Folder["Folder"]

      subgraph PA["Service Perimeter A"]
        P0["base<br/>(Cloud Build mainline + Shared VPC host)"]
        P2["guest_a<br/>(Shared VPC service project, host=base)"]
      end

      subgraph PB["Service Perimeter B"]
        P4["guest_b<br/>(Shared VPC service project, host=base)"]
      end

    end
  end

  AccessPolicy["folder-scoped<br/>Access Policy"]
  AccessPolicy --- PA
  AccessPolicy --- PB
```

### Access Policy / Perimeter

- **folder-scoped Access Policy**: `parent = organizations/<ORG_ID>`,
  `scopes = ["folders/<FOLDER_ID>"]`。組織全体ではなくフォルダ単位に閉じる。
  実値は `terraform.tfvars` で渡し、コードや README には記載しない。
- **Perimeter A**: `base` (Cloud Build mainline + Shared VPC host) + `guest_a` の 2 projects。
  メインの検証パスはここに集約。
- **Perimeter B**: `guest_b` のみ。Case 4 の境界分離検証専用。
- **Ingress policy**: 両 perimeter とも `user:<管理者メアド>` を identity として
  許可 (Terraform apply / 開発者操作のため)。
  `source.access_level = "*"` / `service_name = "*"` で any。

### プロジェクト一覧

| project | 役割 | 所属 perimeter | VPC | Private Pool | 主に使う Case |
|---|---|---|---|---|---|
| `base` (`cbvpcsc-host-<suffix>`) | Cloud Build mainline + Shared VPC host | A | 自 project VPC (Case 0/1/2 用) + Shared 公開 host VPC (Case 3/4 用) | `pool-base` (peering 有), `pool-noPeering` (peering 無, Case 1), `pool-defaultRange` (peered_network_ip_range 省略, Case 2b) | 0, 1, 2a, 2b |
| `guest_a` (`cbvpcsc-guesta2-<suffix>`) | Shared VPC service project (host=base) / Case 3 用 pool | A | base の host VPC を借用 | `pool-guest-a` (Case 3 用) | 3 |
| `guest_b` (`cbvpcsc-guest-b-<suffix>`) | Shared VPC service project (host=base) / Case 4 用 pool | **B** | base の host VPC を借用 | `pool-guest-b` (Case 4 用) | 4 |

### Artifact Registry

- 各 project に **DockerHub mirror (REMOTE)** + **PyPI mirror (REMOTE)** + **build-output (STANDARD)** の 3 repo を配置。
- 各 region で `google_artifact_registry_vpcsc_config.vpcsc_policy = "ALLOW"` を 1 つ設定し、AR の REMOTE 上流 fetch だけは VPC-SC を素通りさせる
  (perimeter の egressPolicies を直接いじらない王道)。

---

## テストケース

### Case 0: ベース (Private Pool が境界内として動く)

**構成**

```mermaid
flowchart LR
  subgraph PA["Service Perimeter A"]
    subgraph P0["base"]
      VPC[(VPC)]
      Pool["Private Pool<br/>(no_external_ip)"]
      AR_DH["AR DockerHub mirror"]
      AR_PyPI["AR PyPI mirror"]
      AR_Out["AR build-output"]
      Pool -.peered.-> VPC
      Pool -- "pull (auto-auth, build SA)" --> AR_DH
      Pool -- "pip via mirror" --> AR_PyPI
      Pool -- "push" --> AR_Out
    end
  end
```

**観点**: Private Pool worker → 同 project の AR は **Ingress policy 不要** で通るか

**期待**:
- Build job が DockerHub mirror から `python:3.12-slim` を pull、PyPI mirror から
  `requests` を install、build-output に push まで成功 (`SUCCESS`)
- Build SA は Ingress identities に含まれていない。それでも通る = pool が境界内扱い

### Case 1: VPC peering 不在 → AR 不通

**構成**

```mermaid
flowchart LR
  subgraph PA["Service Perimeter A"]
    subgraph P0["base"]
      VPC2[(VPC #2)]
      Pool2["Private Pool #2<br/>(peering 未設定)"]
      AR_DH2["AR DockerHub mirror"]
      Pool2 -.X.-> VPC2
      Pool2 -.X.-> AR_DH2
    end
  end
```

**観点**: Service Networking connection が無いと、worker が consumer VPC と繋がらず、私設 DNS ゾーン経由で `pkg.dev` を restricted VIP に解決する経路も成立しない → AR 到達不可

**期待**:
- pool そのものは作れるが、build job 内 `docker pull <AR_image>` が timeout / DNS 解決失敗で **FAILURE**

### Case 2a: VPC 側の reserved IP range が無い → peering 確立不可

**構成**

```mermaid
flowchart LR
  subgraph P0["base"]
    VPC3[(VPC #3)]
    SN["Service Networking connection<br/>(reserved range 無で作成試行)"]
    SN -. fail .-> VPC3
  end
```

**観点**: `google_compute_global_address (purpose=VPC_PEERING)` で /N の reserved CIDR を確保していないと、`google_service_networking_connection` が引数不足で確立できない → 結果として private pool も `peered_network` を指定して作れない

**期待**:
- **Terraform apply 段階で失敗する**ことを記述する static な検証 (Go テストで Build を投入する手前の話)。本ケースは README で原理を示し、リソースは Terraform 側で `count = 0` のコメントアウト形にして「やってみたければ enable」 という形にする

### Case 2b: `peered_network_ip_range` を省略 → default で動く

**構成**

```mermaid
flowchart LR
  subgraph PA["Service Perimeter A"]
    subgraph P0["base"]
      VPC4[(VPC + reserved /20)]
      Pool4["Private Pool<br/>(peered_network_ip_range 省略)"]
      AR_DH["AR DockerHub mirror"]
      Pool4 -.peered.-> VPC4
      Pool4 -- "pull / push" --> AR_DH
    end
  end
```

**観点**: pool の `peered_network_ip_range` を指定しない場合、Cloud Build は default のサブレンジ幅で peering を確立する。指定が必須ではないことを正の挙動として確認

**期待**:
- Build job が SUCCESS。`peered_network_ip_range` 省略でも特別な対応不要

### Case 3: Shared VPC のゲスト側に pool、 同一 perimeter

**構成**

```mermaid
flowchart LR
  subgraph PA["Service Perimeter A"]
    subgraph Host["base (Shared VPC host)"]
      HostVPC[(Host VPC)]
      HostAR["AR (host 側)"]
    end
    subgraph Guest["guest_a"]
      GPool["Private Pool"]
      GuestAR["AR (guest 側)"]
    end
  end
  GPool -.borrows host VPC.-> HostVPC
  GPool -- "pull" --> HostAR
  GPool -- "pull/push" --> GuestAR
```

**観点**: Shared VPC で pool が **service project (guest)** 側にあるとき、host project の AR にも guest project の AR にもアクセスできるか

**期待**:
- guest pool → guest AR 成功 (同 project)
- guest pool → host AR 成功 (同 perimeter なので境界内扱い、Shared VPC で network経路もOK)
- 両方とも SUCCESS

### Case 4: 境界が host / guest で分割（poolはguest, VPCはhost）

**構成**

```mermaid
flowchart LR
  subgraph PA["Service Perimeter A"]
    subgraph HostB["base (Shared VPC host, Perimeter A)"]
      HostBVPC[(Host VPC)]
      HostBAR["AR (host)"]
    end
  end
  subgraph PB["Service Perimeter B (Ingress * / Egress *)"]
    subgraph GuestB["guest_b"]
      GBPool["Private Pool"]
      GuestBAR["AR (guest)"]
    end
  end
  GBPool -.peered.-> HostBVPC
  GBPool -- "?" --> HostBAR
  GBPool -- "?" --> GuestBAR
```

**観点**: pool は guest 側 (perimeter B)、利用する VPC は host 側 (perimeter A)。Ingress / Egress を `*` で全許可しても、 VPC-SC の保護対象は project resource なので、 **pool が居る perimeter B に protected な resource (= guest AR)** 側にしか到達できない、という仮説の検証

**当初の仮説**:
- guest pool → guest AR (同 perimeter B): 成功
- guest pool → host AR (perimeter A): VPC-SC violation で失敗
- 「pool が居る側」 が「境界内」 として優先される

#### 実際に観察された挙動 (本実装での検証結果)

仮説より上位の壁が存在することがわかった:

- **cross-perimeter Shared VPC では Cloud Build Private Pool worker そのものが起動しない**
- guest_b project (Perimeter B) に作った pool は base (Perimeter A) の Shared VPC を借用する構成。 build submit 自体は受理されるが、 worker が永久に **`QUEUED` のまま scheduled されない** (20 分以上待っても変化なし)。
- 結果として、 guest_b pool から **guest_b 自身の AR (同 perimeter B 内)** にすらアクセスできない (worker が動かないため)。 当初の仮説 (cross-perimeter pull だけ deny) より厳しく、 **pool 自体が機能しない**ことが結論。
- 「pool が居る側に通る / VPC が居る側に通る」 という細かい判定以前に、 **境界をまたいだ Shared VPC を Cloud Build Private Pool で使うことはできない**。

**段階的に発生する VPC-SC の壁**:

1. **`google_compute_shared_vpc_service_project` の作成自体**: cross-perimeter (host=A, service=B) では `SECURITY_POLICY_VIOLATED` で deny される (= 最初に当たる壁)。 これは perimeter を一時的に退避するなどして強行作成は可能だが、 仮にできても次の壁にぶつかる。
2. **作成できても build worker が起動しない**: 上記を回避して service association を作っても、 build を submit すると `QUEUED stuck` のまま worker が動かない (今回観察した壁)。

**運用上の示唆**: Shared VPC で host と service project の VPC-SC 境界を分けると、 Cloud Build Private Pool は実用不能になる。 **host / service project は同一境界に揃える** のが必須。 境界分離する要件があるなら、 Shared VPC + Private Pool の組み合わせ自体を諦めて、 各境界に独立した Private Pool + VPC を立てる構成にする必要がある。

---

## ディレクトリ構成 (実装後)

```
samplecodes/cloudbuild-private-pool/
├── README.md                      # 本ファイル
├── 00_providers.tf                # google + google-beta provider
├── 01_variables.tf
├── 03_vpc-sc.tf                   # Access Policy + Perimeter A + Perimeter B
├── 04_projects.tf                 # 新規 project (base / guest_a / guest_b)
├── 04_vpc.tf                      # base 上の VPC (Case 0/1/2 用)
├── 05_artifact_registry.tf        # 各 project の AR repo + vpcsc_config
├── 06_cloudbuild.tf               # base 上の pool (base / noPeering / defaultRange)
├── 07_iam.tf
├── 08_outputs.tf
├── 99_moved.tf                    # state migration 用 moved block
├── private_pool_test.go           # Case 0..4 を Go test で
├── Makefile
├── terraform.tfvars.example
└── .gitignore
```

## Test Plan

| Case | テスト関数 | 期待 status | 結果 |
|---|---|---|---|
| 0 | `TestVPCSCPrivatePool_DockerBuild` | SUCCESS | ✅ PASS |
| 0 (negative) | `TestVPCSCPrivatePool_ExternalAccessDenied` | FAILURE | ✅ PASS (期待通り FAILURE) |
| 1 | `TestCase1_NoPeering_DockerBuildFails` | FAILURE | ✅ PASS (期待通り FAILURE) |
| 2a | (Terraform plan/apply で失敗、Goテスト無し) | — | 未実施 (原理確認のみ) |
| 2b | `TestCase2b_DefaultPeeredRange_DockerBuildSucceeds` | SUCCESS | ✅ PASS |
| 3 | `TestCase3_SharedVPC_GuestPool_PullGuestAR` | SUCCESS | ✅ PASS |
| 3 (cross-AR) | `TestCase3_SharedVPC_GuestPool_PullHostAR` | SUCCESS | ⏸ 未実装 (追加検証候補) |
| 4 | `TestCase4_SplitPerimeter_GuestPool_PullGuestAR` | NOT SUCCESS (worker QUEUED stuck or FAILURE) | ✅ PASS (期待通り cross-perim Shared VPC で worker 起動せず) |
| 4 | `TestCase4_SplitPerimeter_GuestPool_PullHostAR_Denied` | NOT SUCCESS | ✅ PASS |

## 機密情報の取り扱い

- folder ID / project ID / org ID / 管理者メアド / billing account ID はすべて
  `terraform.tfvars` (gitignored) のみに保持
- `terraform.tfvars.example` には placeholder のみ
- README / コード上のサンプルはすべて変数経由で参照する形にとどめ、 git 履歴に
  実値が残らないようにする

---

## 追加検証候補 (本 PR スコープ外)

時間 / コスト / 実用性の理由で本 PR では実装しないが、検証として価値があり
今後追加する候補。気が向いたら別 PR で。

### 候補 A: Shared VPC のホスト側に pool、 ゲストから build を submit (同 perimeter)

```mermaid
flowchart LR
  subgraph PA["Perimeter A"]
    subgraph Host["host (Shared VPC host)"]
      HPool["Private Pool (host 側)"]
      HostVPC[(Host VPC)]
      HostAR["AR (host)"]
    end
    subgraph Guest["guest (service project)"]
      Submit["build submit"]
      GuestAR["AR (guest)"]
    end
  end
  Submit -- "creates build on host pool<br/>(cross-project)" --> HPool
  HPool -- "?" --> HostAR
  HPool -- "?" --> GuestAR
```

**観点**: pool を host project に置き、 build 投入は guest project 側から行う
構成 (cross-project build)。実運用では稀だが、 host project が共有インフラ用、
guest project が業務用、 という組織分割があり得る

**期待**: 両 project の AR にアクセス可 (両方 SUCCESS)

### 候補 B: 候補 A と同パターンで host / guest が境界分離

```mermaid
flowchart LR
  subgraph PA["Perimeter A"]
    subgraph Host["host"]
      HPool["Private Pool"]
      HostVPC[(Host VPC)]
      HostAR["AR (host)"]
    end
  end
  subgraph PB["Perimeter B"]
    subgraph Guest["guest"]
      Submit["build submit"]
      GuestAR["AR (guest)"]
    end
  end
  Submit -- "cross-project + cross-perimeter submit" --> HPool
  HPool -- "?" --> HostAR
  HPool -- "?" --> GuestAR
```

**観点**: 候補 A の境界分離版。pool が host (Perimeter A)、 build 投入が
guest (Perimeter B)。 cross-project と cross-perimeter が重なる
シナリオで、 どちら側の AR に通るか / 両方 deny になるか

**期待 (仮説)**: 候補 A の host 側 (= pool 配置側) の AR に通る、guest側はdeny

### 候補 C: 境界外 project と境界内 VPC を peering したときの境界判定

```mermaid
flowchart LR
  subgraph Outside["VPC-SC 外 project X"]
    XVM["VM (or Cloud Run worker)"]
    XVPC[(VPC X)]
    XVM -.in.-> XVPC
  end
  subgraph PA["Perimeter A (境界内)"]
    subgraph Inside["base"]
      InsideVPC[(VPC)]
      InsideAR["AR (境界内)"]
    end
  end
  XVPC -- "VPC peering<br/>(network 経路は通る)" --- InsideVPC
  XVM -- "?<br/>VPC-SC 上どう判定?" --> InsideAR
```

**観点**: 「Cloud Build private pool は producer VPC と consumer VPC の
**Service Networking peering** で境界内扱いになる」 という挙動と、
**通常の VPC peering** とは区別される。境界外 project X の VPC を
境界内 VPC と通常 VPC peering したとき、 caller (X の VM) は境界内
resource (AR) にアクセスできるか

**仮説**:
- 通常の VPC peering は network 経路を繋ぐだけで、 VPC-SC は **caller の
  project が perimeter resources に含まれているか** で判定する
- caller (X の VM) は境界外 project に属するため、 VPC-SC violation で deny
- つまり「peering したから境界内」 ではなく、 Cloud Build private pool の
  Service Networking 経由は **特別扱い** (consumer project の perimeter内と
  みなされる) という結論になる想定

**ドキュメント**:
- 公式 doc が言及している可能性が高い領域。 実装前に
  `cloud.google.com/vpc-service-controls/docs/share-across-perimeters`
  や private pool VPC-SC ガイドを再確認すると、 仮説が補強できるはず
