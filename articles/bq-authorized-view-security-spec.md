---
title: "BigQuery承認済みビューと承認済みデータセットのセキュリティ仕様を自動テストで検証した"
emoji: "🔐"
type: "tech"
topics: ["bigquery", "googlecloud", "terraform", "go", "security"]
published: false
---

## はじめに

BigQuery の**承認済みビュー（Authorized View）**は、機密データを含むデータセットに対して、ビューを通じて限定的なアクセスを提供する機能です。似た機能として**承認済みデータセット（Authorized Dataset）**もありますが、両者のセキュリティ上の違いについて、ドキュメントだけでは判断しづらい点がありました。

特に気になったのは以下の点です。

- 承認済みビューの SQL を編集したら「再承認」が必要なのか？
- ソースデータへの権限を持たないユーザーが、ビューの SQL を書き換えて機密データを抜き出せてしまわないか？
- 承認済みビューと承認済みデータセットで、権限チェックの挙動は異なるか？

これらの疑問に対して、Terraform + Go テストで検証環境を構築し、自動テストで仕様を実証しました。

## 結論

先に結論をまとめます。

| 操作 | 承認済みビュー | 承認済みデータセット |
|---|:---:|:---:|
| ビュー経由の閲覧 | OK | OK |
| 管理者がビュー SQL を更新後、即反映されるか | OK（再承認不要） | OK（再承認不要） |
| 制限ユーザーがビュー SQL を変更 | 403 拒否 | 403 拒否 |
| 制限ユーザーが新規ビューを作成 | 403 拒否 | 403 拒否 |
| 未承認データセットへの参照 | 403 拒否 | 403 拒否 |
| ネストされた多段参照 | OK | OK |
| ネストのバイパス（中間層を飛ばして直接参照） | 403 拒否 | 403 拒否 |

承認済みビュー・承認済みデータセットのどちらでも、ビューを作成・編集するユーザーは参照先テーブルへの `bigquery.tables.getData` 権限が必要です。権限のないユーザーがビューの SQL を改ざんして機密データを取得することは、どちらの方式でも防がれます。

両者の違いは**管理の粒度**にあります。

- **承認済みビュー**: ビュー単位で個別に承認。新しいビューを追加するたびに参照先データセットの承認リストへの登録が必要
- **承認済みデータセット**: データセット全体を一括で承認。将来追加されるビューも自動的に承認されるため、多数のビューを運用する場合に管理の手間が軽減される

## 検証環境の構成

### 全体像

```mermaid
graph TB
  subgraph "dataset_a（機密データ）"
    table_a["confidential_data<br/>id, name, email, salary"]
  end

  subgraph "承認済みビュー"
    subgraph "dataset_b（公開用）"
      view_b["public_directory ビュー<br/>SELECT id, name, email"]
    end
    subgraph "dataset_c（ネスト検証用）"
      view_c["nested_directory ビュー"]
    end
  end

  subgraph "承認済みデータセット"
    subgraph "dataset_d（一括承認）"
      view_d["ds_public_directory ビュー<br/>SELECT id, name, email"]
    end
    subgraph "dataset_e（ネスト検証用）"
      view_e["ds_nested_directory ビュー"]
    end
  end

  view_b -->|"承認済みビュー<br/>（ビュー単位で承認）"| table_a
  view_c -->|"承認済みビュー"| view_b
  view_d -->|"承認済みデータセット<br/>（データセット単位で承認）"| table_a
  view_e -->|"承認済みデータセット"| view_d
```

### ユーザー構成

| ユーザー | 役割 | 権限 |
|---|---|---|
| user1 | 管理者 | dataset_a〜e すべてに `dataOwner` |
| user2 | 制限ユーザー（承認済みビュー検証用） | dataset_b, dataset_c に `dataViewer` + `dataEditor` |
| user3 | 制限ユーザー（承認済みデータセット検証用） | dataset_d, dataset_e に `dataViewer` + `dataEditor` |

user2, user3 はいずれも **dataset_a（機密データ）への直接権限を持ちません**。

## 承認済みビューのテストケース

### ケース 1: ビュー経由の閲覧（正常系）

```mermaid
sequenceDiagram
    participant user2
    participant dataset_b as dataset_b<br/>public_directory ビュー
    participant dataset_a as dataset_a<br/>confidential_data

    user2->>dataset_b: SELECT id, name, email
    dataset_b->>dataset_a: 承認済みビューとして参照
    dataset_a-->>dataset_b: データ返却
    dataset_b-->>user2: 成功（id, name, email のみ）

    Note over user2,dataset_a: user2 は dataset_a への直接権限なし<br/>承認済みビュー経由でのみアクセス可能
```

### ケース 2: 悪意のある更新（異常系）

```mermaid
sequenceDiagram
    participant user2
    participant dataset_b as dataset_b<br/>public_directory ビュー
    participant dataset_a as dataset_a<br/>confidential_data

    user2->>dataset_b: ビュー SQL を SELECT * に変更
    dataset_b->>dataset_a: 権限チェック: user2 は<br/>dataset_a の bigquery.tables.getData を持つか？

    dataset_a--xdataset_b: 権限なし
    dataset_b--xuser2: 403 Forbidden（保存拒否）

    Note over user2,dataset_a: ビュー編集者が参照先への権限を持たない場合<br/>SQL の保存自体が拒否される
```

### ケース 3: 管理者による更新と再承認の不要性（正常系）

```mermaid
sequenceDiagram
    participant user1
    participant user2
    participant dataset_b as dataset_b<br/>public_directory ビュー
    participant dataset_a as dataset_a<br/>confidential_data

    user1->>dataset_b: ビュー SQL を更新<br/>（カラム追加等）
    dataset_b->>dataset_a: 権限チェック: user1 は dataOwner
    dataset_a-->>dataset_b: OK
    dataset_b-->>user1: 更新成功

    Note over dataset_b: 再承認は不要

    user2->>dataset_b: SELECT（更新後の SQL）
    dataset_b->>dataset_a: 承認済みビューとして参照
    dataset_a-->>dataset_b: データ返却
    dataset_b-->>user2: 成功（更新が即座に反映）
```

### ケース 4: 不正なビュー作成（異常系）

```mermaid
sequenceDiagram
    participant user2
    participant dataset_b as dataset_b
    participant dataset_a as dataset_a<br/>confidential_data

    user2->>dataset_b: dataset_a を参照する<br/>新規ビューを作成
    dataset_b->>dataset_a: 権限チェック: user2 は<br/>dataset_a の bigquery.tables.getData を持つか？

    dataset_a--xdataset_b: 権限なし
    dataset_b--xuser2: 403 Forbidden（作成拒否）

    Note over user2,dataset_a: 承認済みビューが存在するデータセットでも<br/>新規ビュー作成時に参照先への権限チェックが行われる
```

### ケース 5: ネストされた承認（正常系）

```mermaid
sequenceDiagram
    participant user2
    participant dataset_c as dataset_c<br/>nested_directory ビュー
    participant dataset_b as dataset_b<br/>public_directory ビュー
    participant dataset_a as dataset_a<br/>confidential_data

    user2->>dataset_c: SELECT id, name
    dataset_c->>dataset_b: 承認済みビューとして参照
    dataset_b->>dataset_a: 承認済みビューとして参照
    dataset_a-->>dataset_b: データ返却
    dataset_b-->>dataset_c: データ返却
    dataset_c-->>user2: 成功

    Note over user2,dataset_a: 多段構成でも、各層で承認が設定されていれば<br/>チェーン全体が機能する
```

### ケース 6: ネストのバイパス（異常系）

```mermaid
sequenceDiagram
    participant user2
    participant dataset_c as dataset_c<br/>nested_directory ビュー
    participant dataset_a as dataset_a<br/>confidential_data

    user2->>dataset_c: ビュー SQL を変更<br/>中間層を飛ばして dataset_a を直接参照
    dataset_c->>dataset_a: 権限チェック: user2 は<br/>dataset_a の bigquery.tables.getData を持つか？

    dataset_a--xdataset_c: 権限なし
    dataset_c--xuser2: 403 Forbidden（保存拒否）

    Note over user2,dataset_a: 中間層 dataset_b への権限があっても<br/>最深部 dataset_a への直接参照は権限不足で拒否
```

## 承認済みデータセットのテストケース

### ケース 1: データセット経由の閲覧（正常系）

```mermaid
sequenceDiagram
    participant user3
    participant dataset_d as dataset_d<br/>ds_public_directory ビュー
    participant dataset_a as dataset_a<br/>confidential_data

    user3->>dataset_d: SELECT id, name, email
    dataset_d->>dataset_a: 承認済みデータセットとして参照
    dataset_a-->>dataset_d: データ返却
    dataset_d-->>user3: 成功

    Note over user3,dataset_a: 承認済みビューと同様に<br/>dataset_a への直接権限なしでデータ取得可能
```

### ケース 2: 不正なビュー作成（異常系）

```mermaid
sequenceDiagram
    participant user3
    participant dataset_d as dataset_d
    participant dataset_a as dataset_a<br/>confidential_data

    user3->>dataset_d: dataset_a を参照する<br/>新規ビューを作成
    dataset_d->>dataset_a: 権限チェック: user3 は<br/>dataset_a の bigquery.tables.getData を持つか？

    dataset_a--xdataset_d: 権限なし
    dataset_d--xuser3: 403 Forbidden

    Note over user3,dataset_a: 承認済みデータセットでも<br/>ビュー作成者には参照先への権限が必要<br/>（承認済みビューと同じ挙動）
```

### ケース 3: 悪意のある SQL 変更（異常系）

```mermaid
sequenceDiagram
    participant user3
    participant dataset_d as dataset_d<br/>ds_public_directory ビュー
    participant dataset_a as dataset_a<br/>confidential_data

    user3->>dataset_d: ビュー SQL を SELECT * に変更
    dataset_d->>dataset_a: 権限チェック: user3 は<br/>dataset_a の bigquery.tables.getData を持つか？

    dataset_a--xdataset_d: 権限なし
    dataset_d--xuser3: 403 Forbidden（保存拒否）

    Note over user3,dataset_a: 承認済みデータセットでも<br/>ビュー更新者の権限チェックは行われる<br/>（承認済みビューと同じ挙動）
```

### ケース 4: 範囲外データセットへの参照（異常系）

```mermaid
sequenceDiagram
    participant user3
    participant dataset_d as dataset_d
    participant dataset_b as dataset_b<br/>（未承認）

    user3->>dataset_d: dataset_b を参照する<br/>新規ビューを作成
    dataset_d->>dataset_b: 承認チェック: dataset_d は<br/>dataset_b に対して承認されているか？

    dataset_b--xdataset_d: 未承認
    dataset_d--xuser3: 403 Forbidden

    Note over user3,dataset_b: dataset_d は dataset_a のみ承認<br/>dataset_b は承認対象外のため拒否
```

### ケース 5: ネストされた承認（正常系）

```mermaid
sequenceDiagram
    participant user3
    participant dataset_e as dataset_e<br/>ds_nested_directory ビュー
    participant dataset_d as dataset_d<br/>ds_public_directory ビュー
    participant dataset_a as dataset_a<br/>confidential_data

    user3->>dataset_e: SELECT id, name
    dataset_e->>dataset_d: 承認済みデータセットとして参照
    dataset_d->>dataset_a: 承認済みデータセットとして参照
    dataset_a-->>dataset_d: データ返却
    dataset_d-->>dataset_e: データ返却
    dataset_e-->>user3: 成功

    Note over user3,dataset_a: 承認済みデータセットでも<br/>多段構成が正常に機能する
```

### ケース 6: ネストのバイパス（異常系）

```mermaid
sequenceDiagram
    participant user3
    participant dataset_e as dataset_e<br/>ds_nested_directory ビュー
    participant dataset_a as dataset_a<br/>confidential_data

    user3->>dataset_e: ビュー SQL を変更<br/>中間層 dataset_d を飛ばして<br/>dataset_a を直接参照
    dataset_e->>dataset_a: 権限チェック: user3 は<br/>dataset_a の bigquery.tables.getData を持つか？

    dataset_a--xdataset_e: 権限なし
    dataset_e--xuser3: 403 Forbidden（保存拒否）

    Note over user3,dataset_a: dataset_e は dataset_d に対してのみ承認<br/>dataset_a への直接参照は不可
```

## 検証環境の構築

### Terraform

検証環境は Terraform で IaC 管理しています。ポイントは以下の通りです。

- **ローカル State**: 検証用のため Remote Backend は使用しない
- **ランダムサフィックス**: リソース名に `random_id` のサフィックスを付与し、冪等性を確保
- **Service Account Impersonation**: テスト実行者が各ユーザーになりすませるよう、`google_service_account_iam_member` で `roles/iam.serviceAccountTokenCreator` を付与

承認済みビューの承認設定:

```hcl
# ビュー単位で承認（承認済みビュー）
resource "google_bigquery_dataset_access" "authorize_view_b" {
  dataset_id = google_bigquery_dataset.dataset_a.dataset_id

  view {
    project_id = var.project_id
    dataset_id = google_bigquery_dataset.dataset_b.dataset_id
    table_id   = google_bigquery_table.authorized_view.table_id
  }
}
```

承認済みデータセットの承認設定:

```hcl
# データセット単位で承認（承認済みデータセット）
resource "google_bigquery_dataset_access" "authorize_dataset_d" {
  dataset_id = google_bigquery_dataset.dataset_a.dataset_id

  dataset {
    dataset {
      project_id = var.project_id
      dataset_id = google_bigquery_dataset.dataset_d.dataset_id
    }
    target_types = ["VIEWS"]
  }
}
```

`view` ブロックと `dataset` ブロックの違いが、承認済みビューと承認済みデータセットの設定の違いです。

### Go テスト

テストは Go のテーブル駆動テスト（Table-Driven Tests）で実装しています。Service Account Impersonation には `google.golang.org/api/impersonate` パッケージを使用し、JSON 鍵の発行は行っていません。

```go
func newImpersonatedClient(t *testing.T, ctx context.Context, projectID, targetSA string) *bigquery.Client {
    ts, err := impersonate.CredentialsTokenSource(ctx, impersonate.CredentialsConfig{
        TargetPrincipal: targetSA,
        Scopes:          []string{"https://www.googleapis.com/auth/bigquery"},
    })
    // ...
    client, err := bigquery.NewClient(ctx, projectID, option.WithTokenSource(ts))
    return client
}
```

異常系テストでは、Google API の 403 エラーコードを明示的にアサーションしています。

```go
func isPermissionDenied(err error) bool {
    if apiErr, ok := err.(*googleapi.Error); ok {
        return apiErr.Code == 403
    }
    return false
}
```

### 実行方法

```bash
export GOOGLE_CLOUD_PROJECT="your-project-id"
make init    # terraform init
make all     # apply → test → destroy を一括実行
```

## まとめ

承認済みビューと承認済みデータセットのセキュリティ仕様を自動テストで検証した結果、以下のことが分かりました。

1. **再承認は不要**: 管理者がビュー SQL を更新しても、承認の再設定は必要ない。更新後のビューは即座にユーザーに反映される
2. **編集者の権限チェックは常に行われる**: 承認済みビュー・承認済みデータセットのどちらでも、ビューを作成・編集するユーザーには参照先への `bigquery.tables.getData` 権限が必要。これにより SQL 改ざんによるデータ漏洩が防がれている
3. **両者の違いは管理の粒度**: 承認済みビューはビュー単位、承認済みデータセットはデータセット単位。セキュリティレベルは同等で、運用上の管理コストが異なる

検証に使用したコードは [GitHub リポジトリ](https://github.com/katonium/articles/tree/zenn/main/samplecodes/bq-authorized-view) で公開しています。
