# BigQuery 承認済みビュー / 承認済みデータセット 仕様検証ワークスペース

## このワークスペースの目的

BigQuery の「承認済みビュー（Authorized View）」と「承認済みデータセット（Authorized Dataset）」の **セキュリティ仕様の違い** を、自動テストで実証し仕様書として残すためのワークスペースです。

具体的には、以下の問いに対してコードで回答します。

- 承認済みビューを編集したとき、「再承認」は必要なのか？
- ソースデータへの権限を持たないユーザーが、ビューの SQL を書き換えて機密データを抜き出せるか？
- 承認済みビューと承認済みデータセットで、上記の挙動はどう異なるか？

## 検証結果サマリー

### 承認済みビュー vs 承認済みデータセット

| 操作 | 承認済みビュー | 承認済みデータセット |
|---|:---:|:---:|
| ビュー経由の閲覧 | OK | OK |
| 管理者がビュー SQL を更新後、即反映されるか | OK（再承認不要） | OK（再承認不要） |
| **制限ユーザーがビュー SQL を変更** | **403 拒否** | **OK（成功）** |
| **制限ユーザーが新規ビューを作成** | **403 拒否** | **OK（成功）** |
| 未承認データセットへの参照 | 403 拒否 | 403 拒否 |
| ネストされた多段参照 | OK | OK |
| ネストのバイパス（中間層を飛ばして直接参照） | 403 拒否 | 403 拒否 |

### 仕様上の重要な違い

**承認済みビュー** は、ビューを作成・編集するユーザーが参照先データセットへの閲覧権限を持っていることを BigQuery が常にチェックします。これにより、権限のないユーザーがビューの SQL を改ざんして機密データを取得することを防ぎます。

**承認済みデータセット** は、データセット全体を信頼します。承認済みデータセット内であれば、どのユーザーでもビューの作成・SQL 変更が自由に行え、参照先の機密データにアクセスできます。つまり、承認済みデータセットへの書き込み権限（`dataEditor`）を持つユーザーは、実質的に参照先データセットの閲覧権限を持つのと同等です。

## 検証環境の構成

### データセット構成

```
dataset_a (機密データ)
├── confidential_data テーブル: id, name, email, salary
│
├── [承認済みビュー] ──────────────────────────────────────
│   ├── → dataset_b (公開用ビュー)
│   │     └── public_directory ビュー: id, name, email
│   │
│   └── → dataset_c (ネスト検証用)
│         └── nested_directory ビュー → dataset_b.public_directory
│
└── [承認済みデータセット] ────────────────────────────────
    ├── → dataset_d (承認済みデータセット)
    │     └── ds_public_directory ビュー: id, name, email
    │
    └── → dataset_e (ネスト検証用)
          └── ds_nested_directory ビュー → dataset_d.ds_public_directory
```

### ユーザー（Service Account）構成

| ユーザー | 役割 | 権限 |
|---|---|---|
| user1 | 管理者 | dataset_a〜e すべてに `dataOwner` |
| user2 | 制限ユーザー（承認済みビュー検証用） | dataset_b, dataset_c に `dataViewer` + `dataEditor` |
| user3 | 制限ユーザー（承認済みデータセット検証用） | dataset_d, dataset_e に `dataViewer` + `dataEditor` |

- user2, user3 はいずれも **dataset_a への直接権限を持たない**
- テスト実行は **Service Account Impersonation** で各ユーザーになりすまして実施（JSON 鍵は発行しない）

## テストケース一覧

### TestAuthorizedView（承認済みビュー: 6 件）

| # | 種別 | ケース名 | 実行ユーザー | 操作 | 期待結果 |
|---|---|---|---|---|---|
| 1 | 正常系 | ビュー閲覧 | user2 | dataset_b のビューを SELECT | 成功。dataset_a への直接権限なしでデータ取得 |
| 2 | 異常系 | 悪意のある更新 | user2 | ビュー SQL を全カラム参照に書き換え | **403 拒否**。dataset_a への権限がないため保存不可 |
| 3 | 正常系 | 管理者による更新 | user1 → user2 | user1 がビュー SQL を更新後、user2 で閲覧 | 成功。再承認なしで即反映 |
| 4 | 異常系 | 新規ビュー作成 | user2 | dataset_b に dataset_a 参照の新規ビューを作成 | **403 拒否**。作成時または実行時にブロック |
| 5 | 正常系 | ネストされた承認 | user2 | dataset_c → dataset_b → dataset_a の多段ビューを閲覧 | 成功。直近の参照先が承認されていれば閲覧可能 |
| 6 | 異常系 | ネストのバイパス | user2 | dataset_c のビューを直接 dataset_a 参照に変更 | **403 拒否**。中間層を飛ばした参照は不可 |

### TestAuthorizedDataset（承認済みデータセット: 6 件）

| # | 種別 | ケース名 | 実行ユーザー | 操作 | 期待結果 |
|---|---|---|---|---|---|
| 1 | 正常系 | データセット経由の閲覧 | user3 | dataset_d のビューを SELECT | 成功。dataset_a への直接権限なしでデータ取得 |
| 2 | **正常系** | **新規ビュー作成** | user3 | dataset_d に dataset_a 参照の新規ビューを作成 | **成功（承認済みビューとの違い）**。salary 含む全カラム取得可能 |
| 3 | **正常系** | **ビュー SQL 変更** | user3 | dataset_d 内のビュー SQL を全カラム参照に変更 | **成功（承認済みビューとの違い）**。データセット全体が信頼されている |
| 4 | 異常系 | 範囲外アクセス | user3 | dataset_d のビューで未承認の dataset_b を参照 | **403 拒否**。承認はデータセット単位で行われる |
| 5 | 正常系 | ネスト | user3 | dataset_e → dataset_d → dataset_a の多段ビューを閲覧 | 成功 |
| 6 | 異常系 | ネストバイパス | user3 | dataset_e のビューを直接 dataset_a 参照に変更 | **403 拒否**。直接承認されていないため不可 |

## 前提条件

- Google Cloud プロジェクトが作成済みであること
- Terraform >= 1.5 がインストール済みであること
- Go >= 1.23 がインストール済みであること
- テスト実行者に以下の権限が付与されていること:
  - `roles/iam.serviceAccountTokenCreator`（Service Account Impersonation 用）
  - `roles/bigquery.admin` または同等の権限（リソース作成用）

## 実行方法

```bash
# 環境変数にプロジェクト ID を設定
export GOOGLE_CLOUD_PROJECT="your-project-id"

# 初期化
make init

# 一括実行（apply → test → destroy）
make all

# 個別実行
make apply    # Terraform でリソース作成
make test     # Go テスト実行
make destroy  # リソース削除
```

## ファイル構成

```
bq-authorized-view/
├── README.md                    # このファイル
├── Makefile                     # ビルド・テスト・デプロイの自動化
├── main.tf                      # Terraform リソース定義
├── variables.tf                 # Terraform 変数定義
├── outputs.tf                   # Terraform 出力定義
├── terraform.tfvars.example     # 変数設定のサンプル
├── go.mod                       # Go モジュール定義
├── authorized_view_test.go      # テストコード
└── .gitignore
```

## 設計上の判断

- **State 管理**: 検証用のためローカル管理（Remote Backend なし）
- **認証方式**: Service Account Impersonation を使用（JSON 鍵の発行は禁止）
- **冪等性**: リソース名にランダムサフィックスを付与し、複数回実行しても衝突しない
- **テスト手法**: Go のテーブル駆動テスト（Table-Driven Tests）でアサーションにエラーコード（403）を明記
