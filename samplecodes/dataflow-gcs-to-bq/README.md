# Dataflow GCS → BigQuery 仕様検証ワークスペース

## このワークスペースの目的

GCS に配置された各種フォーマットのファイルを Dataflow (Apache Beam) で読み込み、SQL / Python の両方の方式でデータクレンジングして BigQuery に投入する、という一連の挙動を、Terraform + Go テストで宣言的に検証する。

具体的には以下の問いに自動テストで答える。

- CSV / TSV / JSON / gzip 圧縮 CSV を Dataflow から透過的に読めるか？
- Apache Beam Python SDK の `SqlTransform` (Calcite SQL via cross-language) はどこまで使えるか？
- 同じパイプラインの中で SQL 変換と Python 変換を混在させて、それぞれの結果を別の BQ テーブルに書き込めるか？
- カラム削除 / UTC→JST / NULL レコード除去という典型的な変換が、SQL でも Python でも同じ結果になるか？

## 検証範囲

| 軸 | 値 |
|---|---|
| 入力ファイル形式 | CSV / TSV / JSON (NDJSON) / gzip 圧縮 CSV |
| 変換言語 | SQL (Apache Beam SQL = Calcite) / Python (DoFn) |
| 変換ロジック | カラム削除 / UTC→JST / NULL レコード除去 |
| 実行モデル | バッチ (FILE_LOADS) |
| 検証範囲外 | ストリーミング、スキーマ推論、性能、ZetaSQL |

検証マトリクスは **4 ファイル形式 × 2 変換言語 × 3 変換ロジック = 24 ケース**。これを **4 本の Dataflow ジョブ** (1 ファイル形式 = 1 ジョブ) で回し、各ジョブの中で 6 ブランチ (SQL × 3 + Python × 3) を並列実行して 24 個の BigQuery テーブルに書き込む。

## 責務分離

| レイヤー | 責務 |
|---|---|
| **Terraform** | GCS バケット、Artifact Registry、Flex Template (Docker イメージ + spec)、BigQuery dataset と 24 個の宛先テーブル、Dataflow ワーカー SA、IAM。tfstate はローカル |
| **Go テスト** | フィクスチャの GCS アップロード、テーブル truncate、Dataflow ジョブ起動、ジョブ完了待機、BigQuery アサーション、後片付け |
| **Dataflow パイプライン** | 純粋な ETL のみ。テスト用 cleanup / アサーション / 条件分岐は一切持たない |

## アーキテクチャ

```
            Go test
              │
              ├─ TestMain: fixtures/{csv,tsv,json} を GCS にアップロード
              │            (csv.gz は Go 側で gzip して送る)
              ├─ truncate 24 BQ tables
              │
              ├─ Dataflow REST API で 4 ジョブを並列キック
              │     ┌─ csv     job ─┐
              │     ├─ tsv     job ─┤  各ジョブは同じ Flex Template を
              │     ├─ json    job ─┤  --format パラメータ違いで起動
              │     └─ csv_gz  job ─┘
              │
              ├─ JOB_STATE_DONE 待機
              │
              └─ 24 ケースのアサーション
                    SELECT * FROM <宛先テーブル> ORDER BY id
                    → 期待 [][]string と完全一致を検証
```

各 Dataflow ジョブは以下の DAG を実行する。

```
Read(GCS) → Decode(format-specific) → ToInputRow ─┬─ SqlTransform(drop_col)  → BQ
                                                  ├─ SqlTransform(utc_jst)   → BQ
                                                  ├─ SqlTransform(null_drop) → BQ
                                                  ├─ ParDo(PyDropCol)        → BQ
                                                  ├─ ParDo(PyUtcToJst)       → BQ
                                                  └─ ParDo(PyNullDrop)       → BQ
```

## 入力データ (共通)

| id | name | secret | event_at_utc |
|---|---|---|---|
| 1 | Alice | foo | 2026-04-10 01:00:00 |
| 2 | Bob | bar | 2026-04-10 15:30:00 |
| 3 | _NULL_ | baz | 2026-04-10 10:00:00 |
| 4 | Dave | qux | 2026-04-10 23:00:00 |
| 5 | _NULL_ | quux | 2026-04-10 05:00:00 |

## 期待結果

### drop_col (5 行、`secret` 列なし)

すべての行が出力される。`event_at_utc` はそのまま。

### utc_jst (5 行、`event_at_jst` が UTC+9)

| id | event_at_jst (UTC で見た TIMESTAMP) |
|---|---|
| 1 | 2026-04-10 10:00:00 |
| 2 | 2026-04-11 00:30:00 |
| 3 | 2026-04-10 19:00:00 |
| 4 | 2026-04-11 08:00:00 |
| 5 | 2026-04-10 14:00:00 |

### null_drop (3 行、`name` が非 NULL)

id ∈ {1, 2, 4} のみ。

## 前提条件

- Google Cloud プロジェクトが作成済みであること
- Terraform >= 1.5、Go >= 1.23、`gcloud` CLI が利用可能であること
- テスト実行ユーザに以下が付与されていること:
  - `roles/bigquery.admin` (テーブル作成・truncate・クエリ)
  - `roles/dataflow.developer` (ジョブ起動)
  - `roles/storage.admin` (バケット作成・オブジェクト操作)
  - `roles/artifactregistry.admin`
  - `roles/iam.serviceAccountAdmin` および `iam.serviceAccountUser`
  - Cloud Build 利用権限 (`roles/cloudbuild.builds.editor`)
- Application Default Credentials が `gcloud auth application-default login` で設定済みであること

## 実行方法

```bash
export GOOGLE_CLOUD_PROJECT="your-project-id"

make init
make apply    # Terraform で GCS / BQ / SA / Flex Template を作成
make test     # Go テストで Dataflow ジョブ起動 → 24 ケース検証
make destroy  # 後片付け
```

`make all` で apply → test → destroy を一気通貫で実行できる。

## ファイル構成

```
dataflow-gcs-to-bq/
├── README.md
├── Makefile
├── main.tf                # GCS / AR / BQ tables / SA / Flex Template ビルド
├── variables.tf
├── outputs.tf
├── terraform.tfvars.example
├── go.mod
├── dataflow_test.go       # 24 ケースのテーブル駆動テスト
├── pipeline/              # Flex Template (Python) ソース
│   ├── Dockerfile         # JRE 同梱 (SqlTransform 用)
│   ├── metadata.json
│   ├── requirements.txt
│   ├── pipeline.py        # entrypoint
│   └── transforms.py      # 共通変換ロジック (SQL クエリ / DoFn / BQ writer)
└── fixtures/              # Go テストがアップロードするサンプル
    ├── sample.csv
    ├── sample.tsv
    └── sample.json
```

## 設計上の判断

- **State 管理**: 検証用のためローカル管理 (Remote Backend なし)。
- **Flex Template ビルド**: Terraform の `null_resource` から `gcloud builds submit` + `gcloud dataflow flex-template build` を local-exec する。`pipeline/` 配下のソースハッシュを trigger にしているので、コード変更時は `terraform apply` で自動再ビルドされる。
- **Docker ベースイメージ**: `gcr.io/dataflow-templates-base/python311-template-launcher-base` をベースに OpenJDK 17 を追加。`SqlTransform` は cross-language で Java の Calcite に展開されるため JRE が必須。
- **入力スキーマ**: SqlTransform に渡す `InputRow` は NamedTuple。`event_at_utc` は datetime ではなく文字列で運び、SQL 側では `CAST(event_at_utc AS TIMESTAMP)` してから処理する。datetime を NamedTuple に持たせて cross-language の Row に変換するとハマったため、素直な実装にしている。
- **JST 変換 (SQL)**: Calcite の `CONVERT_TIMEZONE` などのタイムゾーン関数はランナー依存があり、Beam SQL 上では動作が不安定。`TIMESTAMPADD(HOUR, 9, ...)` で「JST = UTC+9 の常時オフセット」を素直に表現する。
- **JST 変換 (Python)**: 標準 `zoneinfo` の `Asia/Tokyo` を使う。
- **テーブル隔離**: 宛先テーブルは Terraform で 24 個固定で作成し、Go テストの前後で TRUNCATE して使い回す。テーブル作成は Terraform、データ操作は Go、という責務分離。
- **gzip フィクスチャ**: バイナリをコミットしたくないので `fixtures/sample.csv` を Go テストが gzip して GCS に送る。Dataflow 側は `ReadFromText(compression_type=AUTO)` で `.csv.gz` 拡張子から自動判別する。
