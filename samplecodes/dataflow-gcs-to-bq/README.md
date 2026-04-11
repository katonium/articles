# Dataflow GCS → BigQuery 仕様検証ワークスペース (Beam YAML)

## このワークスペースの目的

「Dataflow で GCS のファイルをちょっとクレンジングして BigQuery に投入したい」という典型的なユースケースを、**Python を書かずに / Docker をビルドせずに / 宣言的に** どこまでやれるかを Terraform + Go テストで検証する。

具体的には以下の問いに自動テストで答える。

- CSV / TSV / JSON / gzip 圧縮 CSV を Beam YAML だけで透過的に読めるか？
- Sql transform (Calcite SQL) でカラム削除 / UTC→JST / NULL レコード除去のような典型的なクレンジングが書けるか？
- 同じ Beam YAML の中でインライン Python (MapToFields / Filter / PyTransform) を使うとどこまで書けるか？
- Docker イメージのビルドを一切せずに、`gcloud dataflow yaml run` だけで運用できるか？

## 検証範囲

| 軸 | 値 |
|---|---|
| 入力ファイル形式 | CSV / TSV / JSON (NDJSON) / gzip 圧縮 CSV |
| 変換言語 | Sql ノード (Calcite SQL) / インライン Python (MapToFields / Filter / PyTransform) |
| 変換ロジック | カラム削除 / UTC→JST / NULL レコード除去 |
| 実行モデル | バッチ |
| **検証範囲外** | カスタム Flex Template、ストリーミング、ZetaSQL |

検証マトリクスは **5 本の Beam YAML パイプライン × 3 変換 = 15 ケース**:

| パイプライン | 入力ファイル | 変換 |
|---|---|---|
| `pipelines/csv.yaml` | sample.csv | Sql × 3 |
| `pipelines/tsv.yaml` | sample.tsv | Sql × 3 |
| `pipelines/json.yaml` | sample.json (NDJSON) | Sql × 3 |
| `pipelines/csv_gz.yaml` | sample.csv.gz | Sql × 3 |
| `pipelines/csv_python.yaml` | sample.csv | MapToFields(py) + Filter(py) + PyTransform |

各パイプラインは 1 つの入力を 3 ブランチに分岐させて、それぞれ別の BQ テーブルに書き込む。

## 責務分離

| レイヤー | 責務 |
|---|---|
| **Terraform** | GCS バケット、Beam YAML を GCS にアップロード、BigQuery dataset と 15 個の宛先テーブル、Dataflow ワーカー SA、IAM。tfstate はローカル |
| **Go テスト** | フィクスチャの GCS アップロード、テーブル truncate、`gcloud dataflow yaml run` で 5 ジョブ並列起動、Dataflow REST API でジョブ完了待機、BigQuery アサーション、後片付け |
| **Beam YAML** | 純粋な ETL 宣言のみ。テスト用 cleanup / アサーション / 条件分岐は一切持たない |

検証対象 (YAML) にテスト用ロジックを 1 行も入れないことで、「**Go テストが通る = YAML が宣言通りに動いている**」という保証になる。

## アーキテクチャ

```
            Go test
              │
              ├─ TestMain: fixtures/{csv,tsv,json} を GCS にアップロード
              │            (csv.gz は Go 側で gzip して送る)
              ├─ TRUNCATE 15 BQ tables
              │
              ├─ exec.Command で gcloud dataflow yaml run × 5 並列
              │     ┌─ csv.yaml         job ─┐
              │     ├─ tsv.yaml         job ─┤  Google が提供する
              │     ├─ json.yaml        job ─┤  YAML runner Flex Template
              │     ├─ csv_gz.yaml      job ─┤  が裏で動く (Docker は自作しない)
              │     └─ csv_python.yaml  job ─┘
              │
              ├─ Dataflow REST API で JOB_STATE_DONE 待機
              │
              └─ 15 ケースのアサーション
                    SELECT * FROM <宛先テーブル> ORDER BY id
                    → 期待 [][]string と完全一致を検証
```

各 YAML パイプラインは下記の DAG を実行する。

```
ReadFromCsv / ReadFromJson ─┬─ Sql(drop_col)  → WriteToBigQuery
                            ├─ Sql(utc_jst)   → WriteToBigQuery
                            └─ Sql(null_drop) → WriteToBigQuery
```

`csv_python.yaml` のみ Sql ノードの代わりに MapToFields / PyTransform / Filter を使う。

## 入力データ (共通)

| id | name | secret | event_at_utc |
|---|---|---|---|
| 1 | Alice | foo | 2026-04-10 01:00:00 |
| 2 | Bob | bar | 2026-04-10 15:30:00 |
| 3 | _NULL_ | baz | 2026-04-10 10:00:00 |
| 4 | Dave | qux | 2026-04-10 23:00:00 |
| 5 | _NULL_ | quux | 2026-04-10 05:00:00 |

## 期待結果

### `*_drop_col` (5 行、`secret` 列なし)

すべての行が出力される。`event_at_utc` はそのまま。

### `*_utc_jst` (5 行、`event_at_jst` が UTC+9)

| id | event_at_jst (UTC で見た TIMESTAMP) |
|---|---|
| 1 | 2026-04-10 10:00:00 |
| 2 | 2026-04-11 00:30:00 |
| 3 | 2026-04-10 19:00:00 |
| 4 | 2026-04-11 08:00:00 |
| 5 | 2026-04-10 14:00:00 |

### `*_null_drop` (3 行、`name` が非 NULL)

id ∈ {1, 2, 4} のみ。

## 前提条件

- Google Cloud プロジェクトが作成済み
- Terraform >= 1.5、Go >= 1.23、`gcloud` CLI が利用可能
- テスト実行ユーザに以下が付与されていること:
  - `roles/bigquery.admin`
  - `roles/dataflow.developer`
  - `roles/storage.admin`
  - `roles/iam.serviceAccountAdmin` および `iam.serviceAccountUser`
- Application Default Credentials が `gcloud auth application-default login` で設定済み

**注**: Artifact Registry / Cloud Build は不要。Docker イメージを自作しないため。

## 実行方法

```bash
export GOOGLE_CLOUD_PROJECT="your-project-id"

make init
make apply    # GCS / BQ / SA / YAML upload
make test     # Go テストで Dataflow ジョブ起動 → 15 ケース検証
make destroy
```

`make all` で apply → test → destroy を一気通貫で実行できる。

## ファイル構成

```
dataflow-gcs-to-bq/
├── README.md
├── Makefile
├── main.tf                # GCS / BQ tables / SA / YAML upload
├── variables.tf
├── outputs.tf
├── terraform.tfvars.example
├── go.mod
├── dataflow_test.go       # 15 ケースのテーブル駆動テスト
├── pipelines/             # Beam YAML パイプライン (Source of Truth)
│   ├── csv.yaml
│   ├── tsv.yaml
│   ├── json.yaml
│   ├── csv_gz.yaml
│   └── csv_python.yaml    # インライン Python 版
└── fixtures/              # Go テストがアップロードするサンプル
    ├── sample.csv
    ├── sample.tsv
    └── sample.json
```

## 設計上の判断

- **State 管理**: 検証用のためローカル管理 (Remote Backend なし)。
- **パイプライン定義は Beam YAML**: Job Builder GUI が出力する形式と同じ。リポジトリで版管理 → Terraform で GCS にアップロード → Go テストから `gcloud dataflow yaml run` で起動、という流れにすることで、GUI から始めて宣言にしまう実運用フローと同じ形になる。
- **Docker ビルドなし**: Google が提供する YAML runner Flex Template が裏で動く。自分で Docker イメージをビルドしないので Artifact Registry も Cloud Build も不要。
- **TSV と gzip CSV は Job Builder GUI には出てこない**: ただし `delimiter: "\t"` と `compression: gzip` を YAML に手で 1 行足せば対応できる。GUI から書き始めて軽く編集する、という Beam YAML らしい使い方。
- **JST 変換 (SQL)**: Calcite の `TIMESTAMPADD(HOUR, 9, ...)` で素直に書く。`CONVERT_TIMEZONE` などのタイムゾーン関数はランナー依存があり Beam SQL では避ける。
- **JST 変換 (Python)**: `PyTransform` の `__callable__` で `from datetime import timedelta` を import 込みで書く。1 行で済まない処理は MapToFields ではなく PyTransform を使うのが楽。
- **gzip フィクスチャ**: バイナリをコミットしたくないので `fixtures/sample.csv` を Go テストが gzip して GCS に送る。
- **テーブル隔離**: 宛先テーブルは Terraform で 15 個固定で作成し、Go テストの前後で TRUNCATE して使い回す。
