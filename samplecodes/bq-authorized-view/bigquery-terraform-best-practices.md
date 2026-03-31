# BigQuery × Terraform ベストプラクティス — Google公式ガイドライン準拠

## 1. Terraformで管理すべきリソース

### 必須（インフラレベル）

| Terraformリソース | 理由 |
|---|---|
| **`google_bigquery_dataset`** | データセットはアクセス境界・ロケーション・デフォルト設定を定義するコア。ロケーションは作成後変更不可のためIaC管理が必須。Google公式モジュール・全アーキテクチャBlueprintで採用。 |
| **`google_bigquery_dataset_access`** | Authorized View/Dataset/Routineの個別管理。`_iam_policy`を使うとAuthorized Viewの権限が上書きされるため、Googleは明確にこちらを推奨。 |
| **`google_bigquery_dataset_iam_binding` / `_member`** | IAMロール付与。`_policy`は使わない（Authorized Viewと競合）。 |
| **`google_bigquery_reservation`** | スロット予約（容量計画）。コスト管理に直結。 |
| **`google_bigquery_reservation_assignment`** | プロジェクト/フォルダへの予約割り当て。 |
| **`google_bigquery_capacity_commitment`** | コミットスロット購入（Editions課金）。財務に関わるため必須。 |
| **`google_bigquery_bi_reservation`** | BI Engineメモリ予約。 |
| **`google_bigquery_connection`** | 外部接続（Cloud SQL, Spanner, GCS等）。 |
| **`google_bigquery_data_transfer_config`** | スケジュールされたデータ転送パイプライン。 |
| **`google_access_context_manager_service_perimeter`** | VPC SC境界。Google公式の`terraform-google-vpc-service-controls`モジュールが存在。 |
| **`google_access_context_manager_access_level`** | VPC SCアクセスレベル。セキュリティポリシーはバージョン管理必須。 |
| **`google_data_catalog_taxonomy` / `_policy_tag`** | カラムレベルセキュリティの分類・ポリシータグ。 |

### 条件付き推奨（スキーマレベル）

| リソース | 推奨条件 |
|---|---|
| **`google_bigquery_table`**（ランディング/ステージングテーブル） | プラットフォームチームが管理するスキーマが安定したテーブル。`deletion_protection = true`を設定。 |
| **`google_bigquery_table`**（View） | インフラ所有のViewはTerraform管理可。分析チーム所有のViewはdbt/Dataform推奨。 |
| **`google_bigquery_routine`** | 共有ユーティリティUDFはTerraform管理。分析用UDFはdbt/Dataform。 |

---

## 2. Terraformで管理すべきでないリソース

| リソース | 理由 | 推奨ツール |
|---|---|---|
| **変換レイヤーのテーブル/View** | ELT/ETLパイプラインの出力。スキーマがビジネスロジックで頻繁に変更され、TF stateとの競合が発生。 | **Dataform**（GCPネイティブ、無料）/ **dbt** |
| **データの投入・ロード** | Terraformは宣言的インフラツール。データロードは不可。 | Data Transfer Service, Dataflow, Composer |
| **アドホック/探索テーブル** | アナリストがサンドボックスで作成。変更頻度が極めて高い。 | Dataset側で`default_table_expiration_ms`を設定して自動削除 |
| **クエリ結果/一時テーブル** | 一時的な存在。 | BigQueryが自動管理 |
| **アプリチーム所有の頻繁に変わるスキーマ** | スプリントごとに複数回変更 → TF stateがボトルネック。 | アプリCI/CDでスキーママイグレーション |

---

## 3. 推奨アーキテクチャ：責務の分離

```
Terraform管理:                    パイプラインツール管理:
─────────────                    ─────────────────────
Dataset                          変換テーブル/View
ランディング/ステージングテーブル       派生/マートテーブル
IAM & アクセス制御                  カラムへのポリシータグ適用
Reservation & 容量                データロード & オーケストレーション
VPC Service Controls              クエリスケジューリング
Connection                       アドホック分析
Data Transfer Config
Dataform/dbt インフラ
```

---

## 4. VPC Service Controls — 詳細ガイダンス

Google公式モジュール（`terraform-google-vpc-service-controls`）の推奨事項：

- **必ずdry-runモードでテスト**してからenforced perimeter適用
- **隔離されたサンドボックスプロジェクト**でテスト後に本番適用
- `use_explicit_dry_run_spec = true`でdry-runとenforced設定を同一リソースで管理
- 組織レベルでAccess Policyを定義（フォルダ/プロジェクトスコープも可）
- 大規模運用では`for_each`で複数Access Level/Perimeterを管理

---

## 5. 注意すべき落とし穴

| 問題 | 詳細 |
|---|---|
| **Authorized Viewの競合** | `google_bigquery_dataset.access`ブロックと`google_bigquery_dataset_access`リソースを混在させない。ポリシーstateが競合する。 |
| **スキーマドリフト** | テーブルスキーマのJSON比較が脆弱。フィールド順序やホワイトスペースの変更でdiffが発生。外部管理スキーマには`lifecycle { ignore_changes = [schema] }`を使用。 |
| **`deletion_protection`バグ** | `false`→`true`変更時にin-place updateではなくdestroy-recreateが発生するプロバイダバージョンあり（[Issue #17357](https://github.com/hashicorp/terraform-provider-google/issues/17357)）。 |
| **Datasetロケーション不変** | 作成後にロケーション変更不可。変更には破棄→再作成（データ消失）が必要。 |
| **`_iam_policy`のリスク** | `google_bigquery_dataset_iam_policy`はAuthorized Viewの権限を消去する。`_binding`か`_member`を使用。 |

---

## 6. Google公式リファレンス

| ドキュメント | URL |
|---|---|
| Managing IaC with Terraform | https://cloud.google.com/docs/terraform/resource-management/managing-infrastructure-as-code |
| Terraform Best Practices (Style & Structure) | https://docs.cloud.google.com/docs/terraform/best-practices/general-style-structure |
| Confidential Data Warehouse Blueprint | https://docs.cloud.google.com/architecture/blueprints/confidential-data-warehouse-blueprint |
| Data Mesh Design (Architecture Center) | https://docs.cloud.google.com/architecture/design-self-service-data-platform-data-mesh |
| BigQuery Authorized Views + Terraform (Blog) | https://cloud.google.com/blog/products/infrastructure/iam-policy-for-bigquery-dataset-authorized-views-terraform/ |
| 公式BigQuery Terraformモジュール | https://github.com/terraform-google-modules/terraform-google-bigquery |
| 公式VPC SC Terraformモジュール | https://github.com/terraform-google-modules/terraform-google-vpc-service-controls |
| Secured Data Warehouse Terraform | https://github.com/GoogleCloudPlatform/terraform-google-secured-data-warehouse |
