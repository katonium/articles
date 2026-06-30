# Parquet → BigQuery 日付文字列ロード検証

「**Parquet を BigQuery にロードするとき、日付文字列 (STRING) のカラムを
DATETIME / TIMESTAMP / DATE カラムへ直接変換ロードできるか**」を Go テストで実証的に検証する。

## 結論 (TL;DR)

| ソース形式 | スキーマ指定方法 | STRING 日付 → 日付型 | 結果 |
|-----------|----------------|--------------------|------|
| **Parquet** | テーブル事前作成 (型付き) + 追記 | TIMESTAMP/DATETIME/DATE | ❌ **失敗** |
| **Parquet** | ロードジョブに明示スキーマ | TIMESTAMP | ❌ **失敗** |
| **Parquet** | autodetect | (推論) | STRING のまま (変換されない) |
| **CSV** | 明示スキーマ | TIMESTAMP | ✅ **成功** |
| **CSV** | autodetect | (推論) | ✅ **TIMESTAMP と自動推論** |

- **Parquet では STRING → 日付型の変換ロードはできない。** 明示スキーマでも autodetect でもダメ。
- 一方、共有された記事 ([classmethod](https://dev.classmethod.jp/articles/bigquery-timestamp-timezone-format/) /
  [Qiita](https://qiita.com/shimizu_toshihiko/items/1fa953261eef8e227da3)) が説明しているのは **CSV/JSON** の挙動で、
  こちらは「型なしテキストをローダがパースする」ため STRING 日付 → TIMESTAMP が成立する。
- **根本原因**: Parquet は self-describing (型がファイルに埋まっている) フォーマットで、
  BigQuery はファイル側の物理型を正とする。STRING(BYTE_ARRAY) を再パースして
  TIMESTAMP に変換する経路が存在しない。CSV/JSON はソースが「型なしテキスト」なので
  ローダがスキーマ/推論に従って文字列をパースできる。

### Parquet でやりたい場合の選択肢

1. **生成側で日付を正しい論理型にする** … Parquet の TIMESTAMP(micros) 論理型で書き出せば
   TIMESTAMP 列へそのまま入る (case5)。
2. **2 段階ロード** … STRING のまま一旦ロードし、`SAFE_CAST(col AS TIMESTAMP)` で
   型付きテーブルへ変換する (case6)。

## 実際に観測されたエラー文言

```
# case1-3: テーブルを型付きで先に作って Parquet を追記
Provided Schema does not match Table <table>. Field ts_str has changed type from TIMESTAMP to STRING

# case7: ロードジョブにスキーマを明示指定
Error while reading data, error message:
Parquet column 'ts_str' has type BYTE_ARRAY which does not match the target cpp_type INT64.
```

ジョブにスキーマを明示すると BigQuery はそれを受け取るが、Parquet の物理型 (BYTE_ARRAY) を
TIMESTAMP の内部表現 (INT64) に**物理レベルで写せず**失敗する。

## テストケース (全 9)

| # | ソース | カラム/方法 | ターゲット | 期待 |
|---|--------|------------|-----------|------|
| 1 | Parquet STRING | テーブル事前作成 | TIMESTAMP | 失敗 |
| 2 | Parquet STRING | テーブル事前作成 | DATETIME | 失敗 |
| 3 | Parquet STRING | テーブル事前作成 | DATE | 失敗 |
| 4 | Parquet STRING | autodetect | (STRING) | 成功・STRING のまま |
| 5 | Parquet INT64+TIMESTAMP論理型 | テーブル事前作成 | TIMESTAMP | 成功・値一致 |
| 6 | Parquet STRING → SAFE_CAST | 2 段階 | TIMESTAMP | 成功・値一致 |
| 7 | Parquet STRING | ジョブに明示スキーマ | TIMESTAMP | 失敗 |
| 8 | CSV STRING | 明示スキーマ | TIMESTAMP | 成功・値一致 |
| 9 | CSV STRING | autodetect | (推論) | 成功・TIMESTAMP と推論 |

## 構成

- **インフラ不要**: GCS バケットも Terraform も使わない。インメモリで生成した Parquet/CSV を
  `bigquery.NewReaderSource` でロードジョブに直接流す。
- **データセット/テーブルはテスト内で作成・破棄**: エフェメラルなデータセット
  (`ds_pq_strconv_<unixtime>`) を作り、`defer` で `DeleteWithContents` する。
- Parquet 生成は [`github.com/parquet-go/parquet-go`](https://github.com/parquet-go/parquet-go)。
  struct タグで STRING 列と TIMESTAMP 論理型列を作り分ける。

## 実行方法

```sh
# ADC が必要 (gcloud auth application-default login 済みであること)
export GOOGLE_CLOUD_PROJECT=<your-project>   # 省略時は ADC から自動検出
make test
# または
go test -v -timeout 600s ./...
```

必要な権限: BigQuery Job User + データセット作成権限 (`bigquery.datasets.create`)。
小さなインメモリデータのみを扱うため課金はごく僅か。

## 追補: 整数 (INT32/INT64) → DATE / TIME ロード検証

「STRING ではなく **整数** を DATE / TIME カラムへロードできるか」を別ファイル
(`parquet_int_to_datetime_test.go`, 全 13 ケース) で検証した。

### 結論 (TL;DR)

- **「INT32 だから DATE/TIME になる」わけではない。決め手は Parquet の論理型アノテーション。**
  アノテーションの無い素の整数を BigQuery は **INTEGER** として扱う。
- 型付きテーブルへ「スキーマ指定なし」でロードすると **INTEGER と衝突して失敗**する。
- 一方、**ロードジョブにスキーマを明示指定すると整数 → DATE/TIME は成功する**
  (STRING の明示スキーマが失敗するのと逆。整数は DATE/TIME の内部表現と同じ整数なので再解釈できる)。
- ただし明示スキーマは **整数を BigQuery 内部表現の単位でそのまま解釈するだけで単位変換しない**。
  - DATE 内部単位 = epoch からの日数 / TIME・TIMESTAMP 内部単位 = **マイクロ秒**。
  - 例: ミリ秒のつもりの INT32 を TIME に明示スキーマで入れると、µs として読まれ **1000 倍ズレる**
    (エラーにならず黙って誤値になるので危険)。
- **論理型アノテーションを付ければ単位は自動で吸収される** (`time(millisecond)` なら ms→µs 変換が走る)。

### 型・経路ごとの早見表

| やりたいこと | 推奨 | 理由 |
|------------|------|------|
| DATE | **INT32 + `date` 論理型** | DATE 内部単位は日数。INT32 で十分 (INT64 不要) |
| TIME・アノテーションなし (明示スキーマ) | **INT64 (µs で用意)** | TIME 内部単位は µs。1日=8.64×10¹⁰µs は INT32 上限を超え表現不可 |
| TIME・ms 精度を素直に入れたい | **INT32 + `time(millisecond)` 論理型** | アノテーションが ms→µs 変換を吸収 |

要するに **「アノテーションを付けないなら TIME は INT64(µs)。INT32 を使うなら論理型アノテーション必須」**。
「INT32 そのものがダメ」ではなく「単位を伝えるアノテーションの無い INT32」がダメ、が本質。

### ケース一覧 (全 13)

| # | Parquet 物理型 | アノテーション | 経路 | ターゲット | 結果 |
|---|---------------|--------------|------|-----------|------|
| D1 | INT32 | なし | テーブル事前作成 | DATE | ❌ `changed type from DATE to INTEGER` |
| D2 | INT32 | `date` | テーブル事前作成 | DATE | ✅ `2026-04-10` |
| D3 | INT32 | なし | autodetect | (推論) | INTEGER と推論 (値=日数 20553) |
| D4 | INT32 | `date` | autodetect | (推論) | DATE と推論 |
| D5 | INT32 | なし | ジョブに明示スキーマ | DATE | ✅ `2026-04-10` (整数を日数解釈) |
| T1 | INT64 | なし | テーブル事前作成 | TIME | ❌ `changed type from TIME to INTEGER` |
| T2 | INT64 | `time(micros)` | テーブル事前作成 | TIME | ✅ `01:23:45.678901` |
| T3 | **INT32** | `time(millis)` | テーブル事前作成 | TIME | ✅ `01:23:45.678` (INT32 でも論理型で OK) |
| T4 | INT64 | `time(micros)` | autodetect | (推論) | TIME と推論 |
| T5 | INT32 | なし | テーブル事前作成 | TIME | ❌ `changed type from TIME to INTEGER` |
| T6 | INT32 | なし | autodetect | (推論) | INTEGER と推論 (値=ミリ秒 5025678) |
| T7 | INT64 | なし | ジョブに明示スキーマ | TIME | ✅ `01:23:45.678901` (µs 解釈、意図通り) |
| T8 | INT32 | なし | ジョブに明示スキーマ | TIME | ⚠️ `00:00:05.025678` (ms を µs 解釈し 1000倍ズレ) |

> 明示スキーマ経路 (D5/T7/T8) では、スキーマをハードコードせず
> 望ましい型でテーブルを作成 → `Table.Metadata()` (getTable) から `Schema` を取り出して流用している。

## 参考

- [Loading Parquet data from Cloud Storage | BigQuery](https://docs.cloud.google.com/bigquery/docs/loading-data-cloud-storage-parquet)
- [Parquet LogicalTypes (DATE=int32 days, TIME MILLIS=int32, TIME MICROS=int64)](https://github.com/apache/parquet-format/blob/master/LogicalTypes.md)
- [Conversion functions | BigQuery](https://docs.cloud.google.com/bigquery/docs/reference/standard-sql/conversion_functions)
- [BigQuery で TIMESTAMP のタイムゾーンとフォーマットについて理解する (classmethod)](https://dev.classmethod.jp/articles/bigquery-timestamp-timezone-format/)
- [BigQuery の日時データ型 (Qiita)](https://qiita.com/shimizu_toshihiko/items/1fa953261eef8e227da3)
