// Package parquet_string_to_bq_test は「Parquet を BigQuery にロードするとき、
// 日付文字列 (STRING) のカラムを DATETIME / TIMESTAMP / DATE カラムへ
// 直接変換ロードできるか」を検証する Go テストスイート。
//
// 前提となる仮説 (公式ドキュメント調査より):
//   Parquet は self-describing なフォーマットで、BigQuery はファイル側の型を正とする。
//   CSV/JSON と違い「文字列をパースして型変換」する経路が無いので、
//   ターゲット列を TIMESTAMP/DATETIME/DATE にしても STRING な Parquet 列は
//   型不一致でロードが失敗する、と予想。
//
// テストケース (全 6):
//   case1: STRING "2026-04-10 01:00:00" → TIMESTAMP 列     … 失敗を期待
//   case2: STRING "2026-04-10 01:00:00" → DATETIME 列      … 失敗を期待
//   case3: STRING "2026-04-10"          → DATE 列          … 失敗を期待
//   case4: STRING (autodetect)          → STRING のまま    … 成功 (コントロール)
//   case5: INT64+TIMESTAMP 論理型        → TIMESTAMP 列     … 成功 (ネイティブ型なら通る)
//   case6: STRING を一旦ロード後 SAFE_CAST で別テーブル化   … 成功 (実務的回避策)
//
// インフラは不要: GCS バケットも Terraform も使わず、インメモリで生成した Parquet を
// bigquery.NewReaderSource で直接ロードジョブに流す。データセットとテーブルは
// すべてこのテスト内で作成し、defer で破棄する。必要なのは BigQuery 権限のみ。
package parquet_string_to_bq_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/bigquery"
	"github.com/parquet-go/parquet-go"
	"google.golang.org/api/iterator"
)

const tsLayout = "2006-01-02 15:04:05"

// ──────────────────────────────────────────────
// Parquet 行スキーマ
// ──────────────────────────────────────────────

// strRow は日付文字列を STRING (BYTE_ARRAY + UTF8) として持つ Parquet 行。
type strRow struct {
	ID    int64  `parquet:"id"`
	TsStr string `parquet:"ts_str"`
}

// nativeTsRow は日付を INT64 + TIMESTAMP(microsecond) 論理型として持つ Parquet 行。
type nativeTsRow struct {
	ID       int64 `parquet:"id"`
	TsNative int64 `parquet:"ts_native,timestamp(microsecond)"`
}

// writeParquet は構造体スライスをインメモリ Parquet バイト列へ書き出す。
func writeParquet[T any](rows []T) ([]byte, error) {
	var buf bytes.Buffer
	w := parquet.NewGenericWriter[T](&buf)
	if _, err := w.Write(rows); err != nil {
		_ = w.Close()
		return nil, fmt.Errorf("write rows: %w", err)
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("close writer: %w", err)
	}
	return buf.Bytes(), nil
}

// ──────────────────────────────────────────────
// BigQuery ヘルパー
// ──────────────────────────────────────────────

func newClient(t *testing.T, ctx context.Context) *bigquery.Client {
	t.Helper()
	projectID := os.Getenv("GOOGLE_CLOUD_PROJECT")
	if projectID == "" {
		projectID = bigquery.DetectProjectID // ADC / gcloud から自動検出
	}
	client, err := bigquery.NewClient(ctx, projectID)
	if err != nil {
		t.Fatalf("bigquery client: %v", err)
	}
	return client
}

// createDataset はエフェメラルなデータセットを作成し、破棄用クリーンアップを返す。
func createDataset(t *testing.T, ctx context.Context, client *bigquery.Client) (string, func()) {
	t.Helper()
	dsID := fmt.Sprintf("ds_pq_strconv_%d", time.Now().Unix())
	ds := client.Dataset(dsID)
	if err := ds.Create(ctx, &bigquery.DatasetMetadata{Location: "US"}); err != nil {
		t.Fatalf("create dataset %s: %v", dsID, err)
	}
	t.Logf("created dataset %s", dsID)
	return dsID, func() {
		if err := ds.DeleteWithContents(ctx); err != nil {
			t.Logf("cleanup dataset %s: %v", dsID, err)
		}
	}
}

// createTable は指定スキーマでテーブルを作成する。
func createTable(t *testing.T, ctx context.Context, client *bigquery.Client, dsID, tableID string, schema bigquery.Schema) {
	t.Helper()
	tbl := client.Dataset(dsID).Table(tableID)
	if err := tbl.Create(ctx, &bigquery.TableMetadata{Schema: schema}); err != nil {
		t.Fatalf("create table %s: %v", tableID, err)
	}
}

// loadParquet はインメモリ Parquet バイト列を ReaderSource で直接ロードする。
func loadParquet(ctx context.Context, client *bigquery.Client, dsID, tableID string, data []byte, write bigquery.TableWriteDisposition) error {
	return loadReader(ctx, client, dsID, tableID, data, bigquery.Parquet, nil, false, 0, write)
}

// loadReader は任意フォーマット・任意スキーマでインメモリデータを ReaderSource でロードする。
//   - schema != nil      : ロードジョブにスキーマを明示指定する
//   - autodetect == true : スキーマ自動検出を有効化する (CSV/JSON では型推論が走る)
//   - skipRows           : CSV のヘッダ行スキップ数
func loadReader(
	ctx context.Context, client *bigquery.Client, dsID, tableID string, data []byte,
	format bigquery.DataFormat, schema bigquery.Schema, autodetect bool, skipRows int64,
	write bigquery.TableWriteDisposition,
) error {
	rs := bigquery.NewReaderSource(bytes.NewReader(data))
	rs.SourceFormat = format
	if schema != nil {
		rs.Schema = schema
	}
	rs.AutoDetect = autodetect
	rs.SkipLeadingRows = skipRows
	loader := client.Dataset(dsID).Table(tableID).LoaderFrom(rs)
	loader.WriteDisposition = write
	job, err := loader.Run(ctx)
	if err != nil {
		return fmt.Errorf("loader run: %w", err)
	}
	status, err := job.Wait(ctx)
	if err != nil {
		return fmt.Errorf("job wait: %w", err)
	}
	return status.Err()
}

// queryRows は SQL を実行して結果行を返す。
func queryRows(ctx context.Context, client *bigquery.Client, sql string) ([][]bigquery.Value, error) {
	it, err := client.Query(sql).Read(ctx)
	if err != nil {
		return nil, err
	}
	var rows [][]bigquery.Value
	for {
		var row []bigquery.Value
		err := it.Next(&row)
		if err == iterator.Done {
			break
		}
		if err != nil {
			return rows, err
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// columnType はテーブルの指定カラムの BigQuery 型を返す。
func columnType(t *testing.T, ctx context.Context, client *bigquery.Client, dsID, tableID, column string) bigquery.FieldType {
	t.Helper()
	md, err := client.Dataset(dsID).Table(tableID).Metadata(ctx)
	if err != nil {
		t.Fatalf("table metadata %s: %v", tableID, err)
	}
	for _, f := range md.Schema {
		if f.Name == column {
			return f.Type
		}
	}
	t.Fatalf("column %s not found in table %s", column, tableID)
	return ""
}

// fqTable は `project.dataset.table` 形式の完全修飾名を返す。
func fqTable(client *bigquery.Client, dsID, tableID string) string {
	return fmt.Sprintf("%s.%s.%s", client.Project(), dsID, tableID)
}

// ──────────────────────────────────────────────
// テスト本体
// ──────────────────────────────────────────────

func TestParquetStringToBQ(t *testing.T) {
	ctx := context.Background()
	client := newClient(t, ctx)
	defer client.Close()

	dsID, cleanup := createDataset(t, ctx, client)
	defer cleanup()

	const strVal = "2026-04-10 01:00:00"
	const dateVal = "2026-04-10"

	// ── 失敗を期待する 3 ケース: STRING Parquet → 日付型カラムへ直接ロード ──
	// テーブルを先に型付きで作り、STRING な Parquet を追記ロードして型不一致を観測する。
	directCases := []struct {
		name       string
		tableID    string
		targetType bigquery.FieldType
		value      string
	}{
		{"case1_string_to_TIMESTAMP", "c1_timestamp", bigquery.TimestampFieldType, strVal},
		{"case2_string_to_DATETIME", "c2_datetime", bigquery.DateTimeFieldType, strVal},
		{"case3_string_to_DATE", "c3_date", bigquery.DateFieldType, dateVal},
	}

	for _, tc := range directCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			createTable(t, ctx, client, dsID, tc.tableID, bigquery.Schema{
				{Name: "id", Type: bigquery.IntegerFieldType},
				{Name: "ts_str", Type: tc.targetType},
			})

			pq, err := writeParquet([]strRow{{ID: 1, TsStr: tc.value}})
			if err != nil {
				t.Fatalf("write parquet: %v", err)
			}

			loadErr := loadParquet(ctx, client, dsID, tc.tableID, pq, bigquery.WriteAppend)
			if loadErr == nil {
				// 仮説に反して成功した場合は、実際に入った値を出して気付けるようにする
				rows, _ := queryRows(ctx, client, fmt.Sprintf(
					"SELECT id, ts_str FROM `%s`", fqTable(client, dsID, tc.tableID)))
				t.Fatalf("仮説に反してロード成功: STRING Parquet が %s 列に入った。値=%v",
					tc.targetType, rows)
			}
			// 期待どおり失敗。ただし権限エラー等での偽陽性を防ぐため、
			// 「型不一致」を示す文言が含まれることまで確認する。
			msg := loadErr.Error()
			if !strings.Contains(msg, "does not match") && !strings.Contains(msg, "changed type") {
				t.Fatalf("失敗はしたが型不一致エラーではない (別要因の可能性): %v", loadErr)
			}
			t.Logf("期待どおり失敗 (STRING Parquet → %s 列): %v", tc.targetType, loadErr)
		})
	}

	// ── case4: コントロール。autodetect で STRING のまま入ることを確認 ──
	t.Run("case4_autodetect_keeps_STRING", func(t *testing.T) {
		const tableID = "c4_autodetect"
		pq, err := writeParquet([]strRow{{ID: 1, TsStr: strVal}})
		if err != nil {
			t.Fatalf("write parquet: %v", err)
		}
		// テーブルを事前作成せず、Parquet のスキーマからそのまま作らせる
		if err := loadParquet(ctx, client, dsID, tableID, pq, bigquery.WriteEmpty); err != nil {
			t.Fatalf("autodetect load failed: %v", err)
		}
		if got := columnType(t, ctx, client, dsID, tableID, "ts_str"); got != bigquery.StringFieldType {
			t.Fatalf("ts_str の型が STRING ではない: %s", got)
		}
		rows, err := queryRows(ctx, client, fmt.Sprintf(
			"SELECT ts_str FROM `%s`", fqTable(client, dsID, tableID)))
		if err != nil {
			t.Fatalf("query: %v", err)
		}
		if len(rows) != 1 || rows[0][0] != strVal {
			t.Fatalf("値が一致しない: want %q, got %v", strVal, rows)
		}
		t.Logf("autodetect では ts_str=STRING のまま、値=%q", strVal)
	})

	// ── case5: ネイティブ TIMESTAMP 論理型なら TIMESTAMP 列へ通る ──
	t.Run("case5_native_timestamp_to_TIMESTAMP", func(t *testing.T) {
		const tableID = "c5_native"
		createTable(t, ctx, client, dsID, tableID, bigquery.Schema{
			{Name: "id", Type: bigquery.IntegerFieldType},
			{Name: "ts_native", Type: bigquery.TimestampFieldType},
		})
		want := time.Date(2026, 4, 10, 1, 0, 0, 0, time.UTC)
		pq, err := writeParquet([]nativeTsRow{{ID: 1, TsNative: want.UnixMicro()}})
		if err != nil {
			t.Fatalf("write parquet: %v", err)
		}
		if err := loadParquet(ctx, client, dsID, tableID, pq, bigquery.WriteAppend); err != nil {
			t.Fatalf("native timestamp load failed: %v", err)
		}
		rows, err := queryRows(ctx, client, fmt.Sprintf(
			"SELECT ts_native FROM `%s`", fqTable(client, dsID, tableID)))
		if err != nil {
			t.Fatalf("query: %v", err)
		}
		if len(rows) != 1 {
			t.Fatalf("行数が想定外: %v", rows)
		}
		got, ok := rows[0][0].(time.Time)
		if !ok {
			t.Fatalf("TIMESTAMP として読めない: %T %v", rows[0][0], rows[0][0])
		}
		if !got.Equal(want) {
			t.Fatalf("値が一致しない: want %s, got %s", want.Format(tsLayout), got.Format(tsLayout))
		}
		t.Logf("ネイティブ TIMESTAMP 論理型 → TIMESTAMP 列 成功、値=%s", got.UTC().Format(tsLayout))
	})

	// ── case6: 回避策。STRING でロード → SAFE_CAST で TIMESTAMP 列の別テーブルへ ──
	t.Run("case6_workaround_load_then_safecast", func(t *testing.T) {
		const srcTable = "c6_src"
		const dstTable = "c6_dst"
		pq, err := writeParquet([]strRow{{ID: 1, TsStr: strVal}})
		if err != nil {
			t.Fatalf("write parquet: %v", err)
		}
		if err := loadParquet(ctx, client, dsID, srcTable, pq, bigquery.WriteEmpty); err != nil {
			t.Fatalf("source load failed: %v", err)
		}
		sql := fmt.Sprintf(
			"CREATE OR REPLACE TABLE `%s` AS SELECT id, SAFE_CAST(ts_str AS TIMESTAMP) AS ts FROM `%s`",
			fqTable(client, dsID, dstTable), fqTable(client, dsID, srcTable))
		job, err := client.Query(sql).Run(ctx)
		if err != nil {
			t.Fatalf("CTAS run: %v", err)
		}
		if status, err := job.Wait(ctx); err != nil {
			t.Fatalf("CTAS wait: %v", err)
		} else if err := status.Err(); err != nil {
			t.Fatalf("CTAS status: %v", err)
		}
		if got := columnType(t, ctx, client, dsID, dstTable, "ts"); got != bigquery.TimestampFieldType {
			t.Fatalf("ts 列が TIMESTAMP ではない: %s", got)
		}
		rows, err := queryRows(ctx, client, fmt.Sprintf(
			"SELECT ts FROM `%s`", fqTable(client, dsID, dstTable)))
		if err != nil {
			t.Fatalf("query: %v", err)
		}
		want := time.Date(2026, 4, 10, 1, 0, 0, 0, time.UTC)
		got, ok := rows[0][0].(time.Time)
		if !ok || !got.Equal(want) {
			t.Fatalf("SAFE_CAST 結果が一致しない: want %s, got %v", want.Format(tsLayout), rows)
		}
		t.Logf("回避策 (STRING ロード → SAFE_CAST) 成功、ts=%s", got.UTC().Format(tsLayout))
	})

	// ── case7: Parquet + ロードジョブにスキーマ明示 (テーブル事前作成なし) ──
	// 「明示スキーマなら変換されるのでは?」という直感を否定する経路。
	// Parquet は self-describing なので、ジョブ側で TIMESTAMP を指定しても
	// ファイル側の STRING と衝突して失敗する。
	t.Run("case7_parquet_explicit_job_schema", func(t *testing.T) {
		const tableID = "c7_pq_jobschema"
		pq, err := writeParquet([]strRow{{ID: 1, TsStr: strVal}})
		if err != nil {
			t.Fatalf("write parquet: %v", err)
		}
		schema := bigquery.Schema{
			{Name: "id", Type: bigquery.IntegerFieldType},
			{Name: "ts_str", Type: bigquery.TimestampFieldType},
		}
		loadErr := loadReader(ctx, client, dsID, tableID, pq, bigquery.Parquet, schema, false, 0, bigquery.WriteEmpty)
		if loadErr == nil {
			rows, _ := queryRows(ctx, client, fmt.Sprintf("SELECT id, ts_str FROM `%s`", fqTable(client, dsID, tableID)))
			t.Fatalf("仮説に反してロード成功: Parquet を明示スキーマ(TIMESTAMP)でロードできた。値=%v", rows)
		}
		t.Logf("期待どおり失敗 (Parquet + 明示スキーマ TIMESTAMP): %v", loadErr)
	})

	// ── case8: CSV + スキーマ明示 (TIMESTAMP) ──
	// 同じ日付文字列でも CSV は型なしテキストなので、ローダがパースして TIMESTAMP に変換できる。
	// これが共有された記事 (CSV/JSON 対象) が説明している挙動。
	t.Run("case8_csv_explicit_schema_TIMESTAMP", func(t *testing.T) {
		const tableID = "c8_csv_explicit"
		csv := []byte(fmt.Sprintf("1,%s\n", strVal)) // ヘッダなし
		schema := bigquery.Schema{
			{Name: "id", Type: bigquery.IntegerFieldType},
			{Name: "ts_str", Type: bigquery.TimestampFieldType},
		}
		if err := loadReader(ctx, client, dsID, tableID, csv, bigquery.CSV, schema, false, 0, bigquery.WriteEmpty); err != nil {
			t.Fatalf("CSV 明示スキーマ load failed: %v", err)
		}
		if got := columnType(t, ctx, client, dsID, tableID, "ts_str"); got != bigquery.TimestampFieldType {
			t.Fatalf("ts_str が TIMESTAMP ではない: %s", got)
		}
		rows, err := queryRows(ctx, client, fmt.Sprintf("SELECT ts_str FROM `%s`", fqTable(client, dsID, tableID)))
		if err != nil {
			t.Fatalf("query: %v", err)
		}
		want := time.Date(2026, 4, 10, 1, 0, 0, 0, time.UTC)
		got, ok := rows[0][0].(time.Time)
		if !ok || !got.Equal(want) {
			t.Fatalf("CSV→TIMESTAMP の値が一致しない: want %s, got %v", want.Format(tsLayout), rows)
		}
		t.Logf("CSV + 明示スキーマ → TIMESTAMP 成功、値=%s", got.UTC().Format(tsLayout))
	})

	// ── case9: CSV + Autodetect ──
	// 自動検出だと BigQuery が日付文字列を見て型を推論する。何型になるかを観測する。
	t.Run("case9_csv_autodetect_infers_type", func(t *testing.T) {
		const tableID = "c9_csv_autodetect"
		csv := []byte(fmt.Sprintf("id,ts_str\n1,%s\n", strVal)) // ヘッダあり
		if err := loadReader(ctx, client, dsID, tableID, csv, bigquery.CSV, nil, true, 0, bigquery.WriteEmpty); err != nil {
			t.Fatalf("CSV autodetect load failed: %v", err)
		}
		inferred := columnType(t, ctx, client, dsID, tableID, "ts_str")
		rows, err := queryRows(ctx, client, fmt.Sprintf("SELECT ts_str FROM `%s`", fqTable(client, dsID, tableID)))
		if err != nil {
			t.Fatalf("query: %v", err)
		}
		t.Logf("CSV + autodetect: BigQuery は ts_str を %s と推論、値=%v", inferred, rows[0][0])
		// autodetect は "YYYY-MM-DD hh:mm:ss" を TIMESTAMP と推論するはず (観測で確認)
		if inferred != bigquery.TimestampFieldType && inferred != bigquery.DateTimeFieldType {
			t.Fatalf("想定外の推論型: %s (TIMESTAMP か DATETIME を期待)", inferred)
		}
	})
}
