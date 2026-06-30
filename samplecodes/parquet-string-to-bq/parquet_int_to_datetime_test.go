// このファイルは「Parquet を BigQuery にロードするとき、整数 (INT32/INT64) を
// DATE / TIME カラムへロードできるか」を検証する。STRING 版 (parquet_load_test.go) の
// 姉妹編で、ヘルパー (writeParquet / loadParquet / columnType ...) を共有する。
//
// 検証したい仮説:
//   「INT32 を入れれば DATE になる」わけではない。Parquet は self-describing なので、
//   BigQuery 側の型は Parquet の *論理型アノテーション* で決まる。
//     - アノテーションなしの INT32 → BigQuery は素の INTEGER として扱う (DATE にならない)
//     - INT32 + DATE 論理型        → DATE 列へ入る
//     - INT64/INT32 + TIME 論理型  → TIME 列へ入る (micros=INT64 / millis=INT32 どちらも可)
//
// テストケース (全 13, CSV は対象外):
//   D1: INT32 (annotなし)       → DATE 列 (事前作成)        … 失敗を期待 (INTEGER 扱い→型不一致)
//   D2: INT32 + date 論理型      → DATE 列 (事前作成)        … 成功・値一致
//   D3: INT32 (annotなし)       → autodetect                … INTEGER と推論 (DATE にならない)
//   D4: INT32 + date 論理型      → autodetect                … DATE と推論
//   D5: INT32 (annotなし)       → ジョブに明示スキーマ DATE … 成功 (整数を日数として解釈)
//   T1: INT64 (annotなし)       → TIME 列 (事前作成)        … 失敗を期待
//   T2: INT64 + time(micros)     → TIME 列 (事前作成)        … 成功・値一致
//   T3: INT32 + time(millis)     → TIME 列 (事前作成)        … 成功 (INT32 でも論理型があれば通る)
//   T4: INT64 + time(micros)     → autodetect                … TIME と推論
//   T5: INT32 (annotなし)       → TIME 列 (事前作成)        … 失敗を期待 (物理型は同じ INT32 でも)
//   T6: INT32 (annotなし)       → autodetect                … INTEGER と推論 (TIME にならない)
//   T7: INT64 (annotなし)       → ジョブに明示スキーマ TIME … 成功 (整数を micros として解釈、意図通り)
//   T8: INT32 (annotなし)       → ジョブに明示スキーマ TIME … 成功するが micros 解釈で 1000倍ズレ
//
// 重要な対比:
//   - 事前作成テーブルへ「明示スキーマなし」でロード → 物理型 INTEGER と衝突して失敗 (D1/T1/T5)
//   - 同じ整数でも「ジョブに明示スキーマ」を渡すと成功する (D5/T7/T8)。
//     ただし明示スキーマは整数を BigQuery 内部表現の *単位* (DATE=日数 / TIME=マイクロ秒) で
//     そのまま解釈するだけで単位変換しない。単位がズレた整数は黙って誤値になる (T8)。
//     論理型アノテーションを付ければ単位は自動で吸収される (T3 は millis を正しく変換)。
//   - STRING 版 case7 (明示スキーマでも失敗) との違いは、整数は DATE/TIME の内部表現と
//     同じ整数なので物理的に再解釈できる点。BYTE_ARRAY (STRING) は再解釈できず弾かれる。
//
// 参考: Parquet 論理型と BigQuery 型の対応
//   https://docs.cloud.google.com/bigquery/docs/loading-data-cloud-storage-parquet#parquet_conversions
//   Parquet 論理型仕様 (DATE=int32 days, TIME MILLIS=int32, TIME MICROS=int64):
//   https://github.com/apache/parquet-format/blob/master/LogicalTypes.md
package parquet_string_to_bq_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/bigquery"
	"cloud.google.com/go/civil"
)

// ──────────────────────────────────────────────
// Parquet 行スキーマ (INT32/INT64 を論理型ありなしで作り分け)
// ──────────────────────────────────────────────

// dateRawRow は epoch からの日数を INT32 に「論理型アノテーションなし」で持つ行。
type dateRawRow struct {
	ID      int64 `parquet:"id"`
	DateVal int32 `parquet:"date_val"`
}

// dateAnnotRow は INT32 + DATE 論理型 (epoch からの日数) を持つ行。
type dateAnnotRow struct {
	ID      int64 `parquet:"id"`
	DateVal int32 `parquet:"date_val,date"`
}

// timeRawRow は深夜0時からのマイクロ秒を INT64 に「論理型なし」で持つ行。
type timeRawRow struct {
	ID      int64 `parquet:"id"`
	TimeVal int64 `parquet:"time_val"`
}

// timeRawINT32Row は深夜0時からのミリ秒を INT32 に「論理型なし」で持つ行。
// 物理型は TIME(millis) と同じ INT32 だが、論理型アノテーションが無い点が違う。
type timeRawINT32Row struct {
	ID      int64 `parquet:"id"`
	TimeVal int32 `parquet:"time_val"`
}

// timeMicroRow は INT64 + TIME(microsecond) 論理型を持つ行。
type timeMicroRow struct {
	ID      int64 `parquet:"id"`
	TimeVal int64 `parquet:"time_val,time(microsecond)"`
}

// timeMilliRow は INT32 + TIME(millisecond) 論理型を持つ行 (TIME を INT32 で表す経路)。
type timeMilliRow struct {
	ID      int64 `parquet:"id"`
	TimeVal int32 `parquet:"time_val,time(millisecond)"`
}

// getTableSchema は既存テーブルの Metadata からスキーマを取り出す。
// スキーマをハードコードする代わりに「実テーブルの定義をそのまま流用する」用途。
func getTableSchema(t *testing.T, ctx context.Context, client *bigquery.Client, dsID, tableID string) bigquery.Schema {
	t.Helper()
	md, err := client.Dataset(dsID).Table(tableID).Metadata(ctx)
	if err != nil {
		t.Fatalf("get table metadata %s: %v", tableID, err)
	}
	return md.Schema
}

func TestParquetIntToDateTimeBQ(t *testing.T) {
	ctx := context.Background()
	client := newClient(t, ctx)
	defer client.Close()

	dsID, cleanup := createDataset(t, ctx, client)
	defer cleanup()

	// ── 検証に使う値 ──
	// DATE: 2026-04-10 を epoch からの日数 (INT32) で表現する。
	wantDate := civil.Date{Year: 2026, Month: 4, Day: 10}
	epochDays := int32(time.Date(2026, 4, 10, 0, 0, 0, 0, time.UTC).Unix() / 86400)

	// TIME: 01:23:45.678901 (micros) / 01:23:45.678 (millis)。
	const (
		tH, tM, tS = 1, 23, 45
		microFrac  = 678901 // マイクロ秒の端数
		milliFrac  = 678    // ミリ秒の端数
	)
	microOfDay := int64((tH*3600+tM*60+tS)*1_000_000 + microFrac)
	milliOfDay := int32((tH*3600 + tM*60 + tS) * 1000 + milliFrac)
	wantTimeMicro := civil.Time{Hour: tH, Minute: tM, Second: tS, Nanosecond: microFrac * 1000}
	wantTimeMilli := civil.Time{Hour: tH, Minute: tM, Second: tS, Nanosecond: milliFrac * 1_000_000}

	// 型不一致 (期待どおりの失敗) かどうかを判定する共通ヘルパー。
	assertTypeMismatch := func(t *testing.T, loadErr error, target bigquery.FieldType, gotValueSQL string) {
		t.Helper()
		if loadErr == nil {
			rows, _ := queryRows(ctx, client, gotValueSQL)
			t.Fatalf("仮説に反してロード成功: アノテーションなし整数が %s 列に入った。値=%v", target, rows)
		}
		msg := loadErr.Error()
		if !strings.Contains(msg, "does not match") && !strings.Contains(msg, "changed type") {
			t.Fatalf("失敗はしたが型不一致エラーではない (別要因の可能性): %v", loadErr)
		}
		t.Logf("期待どおり失敗 (アノテーションなし整数 → %s 列): %v", target, loadErr)
	}

	// ── D1: INT32 (annotなし) → DATE 列 (事前作成) … 失敗を期待 ──
	t.Run("caseD1_rawINT32_to_DATE", func(t *testing.T) {
		const tableID = "d1_raw_date"
		createTable(t, ctx, client, dsID, tableID, bigquery.Schema{
			{Name: "id", Type: bigquery.IntegerFieldType},
			{Name: "date_val", Type: bigquery.DateFieldType},
		})
		pq, err := writeParquet([]dateRawRow{{ID: 1, DateVal: epochDays}})
		if err != nil {
			t.Fatalf("write parquet: %v", err)
		}
		loadErr := loadParquet(ctx, client, dsID, tableID, pq, bigquery.WriteAppend)
		assertTypeMismatch(t, loadErr, bigquery.DateFieldType,
			fmt.Sprintf("SELECT id, date_val FROM `%s`", fqTable(client, dsID, tableID)))
	})

	// ── D2: INT32 + date 論理型 → DATE 列 (事前作成) … 成功・値一致 ──
	t.Run("caseD2_dateLogical_to_DATE", func(t *testing.T) {
		const tableID = "d2_date_logical"
		createTable(t, ctx, client, dsID, tableID, bigquery.Schema{
			{Name: "id", Type: bigquery.IntegerFieldType},
			{Name: "date_val", Type: bigquery.DateFieldType},
		})
		pq, err := writeParquet([]dateAnnotRow{{ID: 1, DateVal: epochDays}})
		if err != nil {
			t.Fatalf("write parquet: %v", err)
		}
		if err := loadParquet(ctx, client, dsID, tableID, pq, bigquery.WriteAppend); err != nil {
			t.Fatalf("date 論理型 load failed: %v", err)
		}
		rows, err := queryRows(ctx, client, fmt.Sprintf("SELECT date_val FROM `%s`", fqTable(client, dsID, tableID)))
		if err != nil {
			t.Fatalf("query: %v", err)
		}
		got, ok := rows[0][0].(civil.Date)
		if !ok || got != wantDate {
			t.Fatalf("DATE 値が一致しない: want %s, got %v (%T)", wantDate, rows[0][0], rows[0][0])
		}
		t.Logf("INT32 + date 論理型 → DATE 列 成功、値=%s", got)
	})

	// ── D3: INT32 (annotなし) → autodetect … INTEGER と推論される ──
	t.Run("caseD3_rawINT32_autodetect_is_INTEGER", func(t *testing.T) {
		const tableID = "d3_raw_autodetect"
		pq, err := writeParquet([]dateRawRow{{ID: 1, DateVal: epochDays}})
		if err != nil {
			t.Fatalf("write parquet: %v", err)
		}
		if err := loadParquet(ctx, client, dsID, tableID, pq, bigquery.WriteEmpty); err != nil {
			t.Fatalf("autodetect load failed: %v", err)
		}
		got := columnType(t, ctx, client, dsID, tableID, "date_val")
		rows, _ := queryRows(ctx, client, fmt.Sprintf("SELECT date_val FROM `%s`", fqTable(client, dsID, tableID)))
		t.Logf("アノテーションなし INT32 → BigQuery は date_val を %s と推論、値=%v (epoch からの日数そのまま)", got, rows[0][0])
		if got != bigquery.IntegerFieldType {
			t.Fatalf("想定外: date_val が %s (INTEGER を期待。INT32 単体では DATE にならないはず)", got)
		}
	})

	// ── D4: INT32 + date 論理型 → autodetect … DATE と推論される ──
	t.Run("caseD4_dateLogical_autodetect_is_DATE", func(t *testing.T) {
		const tableID = "d4_date_autodetect"
		pq, err := writeParquet([]dateAnnotRow{{ID: 1, DateVal: epochDays}})
		if err != nil {
			t.Fatalf("write parquet: %v", err)
		}
		if err := loadParquet(ctx, client, dsID, tableID, pq, bigquery.WriteEmpty); err != nil {
			t.Fatalf("autodetect load failed: %v", err)
		}
		got := columnType(t, ctx, client, dsID, tableID, "date_val")
		if got != bigquery.DateFieldType {
			t.Fatalf("想定外: date_val が %s (DATE を期待)", got)
		}
		t.Logf("INT32 + date 論理型 → BigQuery は date_val を DATE と推論")
	})

	// ── T1: INT64 (annotなし) → TIME 列 (事前作成) … 失敗を期待 ──
	t.Run("caseT1_rawINT64_to_TIME", func(t *testing.T) {
		const tableID = "t1_raw_time"
		createTable(t, ctx, client, dsID, tableID, bigquery.Schema{
			{Name: "id", Type: bigquery.IntegerFieldType},
			{Name: "time_val", Type: bigquery.TimeFieldType},
		})
		pq, err := writeParquet([]timeRawRow{{ID: 1, TimeVal: microOfDay}})
		if err != nil {
			t.Fatalf("write parquet: %v", err)
		}
		loadErr := loadParquet(ctx, client, dsID, tableID, pq, bigquery.WriteAppend)
		assertTypeMismatch(t, loadErr, bigquery.TimeFieldType,
			fmt.Sprintf("SELECT id, time_val FROM `%s`", fqTable(client, dsID, tableID)))
	})

	// ── T5: INT32 (annotなし) → TIME 列 (事前作成) … 失敗を期待 ──
	// 物理型は TIME(millis) と同じ INT32 でも、論理型が無いと BigQuery は INTEGER 扱い。
	t.Run("caseT5_rawINT32_to_TIME", func(t *testing.T) {
		const tableID = "t5_raw_int32_time"
		createTable(t, ctx, client, dsID, tableID, bigquery.Schema{
			{Name: "id", Type: bigquery.IntegerFieldType},
			{Name: "time_val", Type: bigquery.TimeFieldType},
		})
		pq, err := writeParquet([]timeRawINT32Row{{ID: 1, TimeVal: milliOfDay}})
		if err != nil {
			t.Fatalf("write parquet: %v", err)
		}
		loadErr := loadParquet(ctx, client, dsID, tableID, pq, bigquery.WriteAppend)
		assertTypeMismatch(t, loadErr, bigquery.TimeFieldType,
			fmt.Sprintf("SELECT id, time_val FROM `%s`", fqTable(client, dsID, tableID)))
	})

	// ── T6: INT32 (annotなし) → autodetect … INTEGER と推論される ──
	t.Run("caseT6_rawINT32_autodetect_is_INTEGER", func(t *testing.T) {
		const tableID = "t6_raw_int32_autodetect"
		pq, err := writeParquet([]timeRawINT32Row{{ID: 1, TimeVal: milliOfDay}})
		if err != nil {
			t.Fatalf("write parquet: %v", err)
		}
		if err := loadParquet(ctx, client, dsID, tableID, pq, bigquery.WriteEmpty); err != nil {
			t.Fatalf("autodetect load failed: %v", err)
		}
		got := columnType(t, ctx, client, dsID, tableID, "time_val")
		rows, _ := queryRows(ctx, client, fmt.Sprintf("SELECT time_val FROM `%s`", fqTable(client, dsID, tableID)))
		t.Logf("アノテーションなし INT32 → BigQuery は time_val を %s と推論、値=%v (深夜0時からのミリ秒そのまま)", got, rows[0][0])
		if got != bigquery.IntegerFieldType {
			t.Fatalf("想定外: time_val が %s (INTEGER を期待。INT32 単体では TIME にならないはず)", got)
		}
	})

	// ── T2: INT64 + time(micros) → TIME 列 (事前作成) … 成功・値一致 ──
	t.Run("caseT2_timeMicros_to_TIME", func(t *testing.T) {
		const tableID = "t2_time_micros"
		createTable(t, ctx, client, dsID, tableID, bigquery.Schema{
			{Name: "id", Type: bigquery.IntegerFieldType},
			{Name: "time_val", Type: bigquery.TimeFieldType},
		})
		pq, err := writeParquet([]timeMicroRow{{ID: 1, TimeVal: microOfDay}})
		if err != nil {
			t.Fatalf("write parquet: %v", err)
		}
		if err := loadParquet(ctx, client, dsID, tableID, pq, bigquery.WriteAppend); err != nil {
			t.Fatalf("time(micros) 論理型 load failed: %v", err)
		}
		rows, err := queryRows(ctx, client, fmt.Sprintf("SELECT time_val FROM `%s`", fqTable(client, dsID, tableID)))
		if err != nil {
			t.Fatalf("query: %v", err)
		}
		got, ok := rows[0][0].(civil.Time)
		if !ok || got.String() != wantTimeMicro.String() {
			t.Fatalf("TIME 値が一致しない: want %s, got %v (%T)", wantTimeMicro, rows[0][0], rows[0][0])
		}
		t.Logf("INT64 + time(micros) 論理型 → TIME 列 成功、値=%s", got)
	})

	// ── T3: INT32 + time(millis) → TIME 列 (事前作成) … 成功 (INT32 経路) ──
	t.Run("caseT3_timeMillisINT32_to_TIME", func(t *testing.T) {
		const tableID = "t3_time_millis"
		createTable(t, ctx, client, dsID, tableID, bigquery.Schema{
			{Name: "id", Type: bigquery.IntegerFieldType},
			{Name: "time_val", Type: bigquery.TimeFieldType},
		})
		pq, err := writeParquet([]timeMilliRow{{ID: 1, TimeVal: milliOfDay}})
		if err != nil {
			t.Fatalf("write parquet: %v", err)
		}
		if err := loadParquet(ctx, client, dsID, tableID, pq, bigquery.WriteAppend); err != nil {
			t.Fatalf("time(millis) 論理型 load failed: %v", err)
		}
		rows, err := queryRows(ctx, client, fmt.Sprintf("SELECT time_val FROM `%s`", fqTable(client, dsID, tableID)))
		if err != nil {
			t.Fatalf("query: %v", err)
		}
		got, ok := rows[0][0].(civil.Time)
		if !ok || got.String() != wantTimeMilli.String() {
			t.Fatalf("TIME 値が一致しない: want %s, got %v (%T)", wantTimeMilli, rows[0][0], rows[0][0])
		}
		t.Logf("INT32 + time(millis) 論理型 → TIME 列 成功、値=%s (INT32 でも論理型があれば TIME になる)", got)
	})

	// ── T4: INT64 + time(micros) → autodetect … TIME と推論される ──
	t.Run("caseT4_timeMicros_autodetect_is_TIME", func(t *testing.T) {
		const tableID = "t4_time_autodetect"
		pq, err := writeParquet([]timeMicroRow{{ID: 1, TimeVal: microOfDay}})
		if err != nil {
			t.Fatalf("write parquet: %v", err)
		}
		if err := loadParquet(ctx, client, dsID, tableID, pq, bigquery.WriteEmpty); err != nil {
			t.Fatalf("autodetect load failed: %v", err)
		}
		got := columnType(t, ctx, client, dsID, tableID, "time_val")
		if got != bigquery.TimeFieldType {
			t.Fatalf("想定外: time_val が %s (TIME を期待)", got)
		}
		t.Logf("INT64 + time(micros) 論理型 → BigQuery は time_val を TIME と推論")
	})

	// ── 明示スキーマ指定経路: 望ましい型のテーブルを先に作り、その Schema を
	//    getTable (Metadata) から取り出してロードジョブの rs.Schema に渡す。
	//    論理型なしの整数でも、ジョブ側で DATE/TIME を指定すれば通るか? を確認する。
	//    STRING 版 case7 では物理型不一致 (BYTE_ARRAY≠INT64) で弾かれたが、
	//    整数 → DATE/TIME は内部表現が同じ整数なので挙動が変わりうる。結果を観測する。

	// ── D5: INT32 (annotなし) + 既存テーブルの DATE スキーマを明示指定 ──
	t.Run("caseD5_rawINT32_explicit_DATE_schema", func(t *testing.T) {
		const tableID = "d5_raw_explicit_date"
		createTable(t, ctx, client, dsID, tableID, bigquery.Schema{
			{Name: "id", Type: bigquery.IntegerFieldType},
			{Name: "date_val", Type: bigquery.DateFieldType},
		})
		schema := getTableSchema(t, ctx, client, dsID, tableID) // getTable から取り出して流用
		pq, err := writeParquet([]dateRawRow{{ID: 1, DateVal: epochDays}})
		if err != nil {
			t.Fatalf("write parquet: %v", err)
		}
		loadErr := loadReader(ctx, client, dsID, tableID, pq, bigquery.Parquet, schema, false, 0, bigquery.WriteAppend)
		if loadErr != nil {
			t.Logf("[観測] INT32(annotなし) + 明示スキーマ DATE → 失敗: %v", loadErr)
			return
		}
		got := columnType(t, ctx, client, dsID, tableID, "date_val")
		rows, _ := queryRows(ctx, client, fmt.Sprintf("SELECT date_val FROM `%s`", fqTable(client, dsID, tableID)))
		t.Logf("[観測] INT32(annotなし) + 明示スキーマ DATE → 成功: 型=%s, 値=%v (epoch日数=%d をそのまま日数解釈)", got, rows[0][0], epochDays)
	})

	// ── T7: INT64 (annotなし) + 既存テーブルの TIME スキーマを明示指定 ──
	t.Run("caseT7_rawINT64_explicit_TIME_schema", func(t *testing.T) {
		const tableID = "t7_raw_explicit_time_i64"
		createTable(t, ctx, client, dsID, tableID, bigquery.Schema{
			{Name: "id", Type: bigquery.IntegerFieldType},
			{Name: "time_val", Type: bigquery.TimeFieldType},
		})
		schema := getTableSchema(t, ctx, client, dsID, tableID)
		pq, err := writeParquet([]timeRawRow{{ID: 1, TimeVal: microOfDay}})
		if err != nil {
			t.Fatalf("write parquet: %v", err)
		}
		loadErr := loadReader(ctx, client, dsID, tableID, pq, bigquery.Parquet, schema, false, 0, bigquery.WriteAppend)
		if loadErr != nil {
			t.Logf("[観測] INT64(annotなし) + 明示スキーマ TIME → 失敗: %v", loadErr)
			return
		}
		got := columnType(t, ctx, client, dsID, tableID, "time_val")
		rows, _ := queryRows(ctx, client, fmt.Sprintf("SELECT time_val FROM `%s`", fqTable(client, dsID, tableID)))
		t.Logf("[観測] INT64(annotなし) + 明示スキーマ TIME → 成功: 型=%s, 値=%v (INT64=%dµs を micros 解釈、意図通り)", got, rows[0][0], microOfDay)
	})

	// ── T8: INT32 (annotなし) + 既存テーブルの TIME スキーマを明示指定 ──
	// 注意: 明示スキーマは「整数を BigQuery の内部表現の単位で解釈する」だけで
	// 単位変換はしない。TIME 内部表現は micros なので、ミリ秒の INT32 を渡すと
	// 1000 倍ずれた値になる (論理型アノテーションがあれば単位を吸収してくれる)。
	t.Run("caseT8_rawINT32_explicit_TIME_schema", func(t *testing.T) {
		const tableID = "t8_raw_explicit_time_i32"
		createTable(t, ctx, client, dsID, tableID, bigquery.Schema{
			{Name: "id", Type: bigquery.IntegerFieldType},
			{Name: "time_val", Type: bigquery.TimeFieldType},
		})
		schema := getTableSchema(t, ctx, client, dsID, tableID)
		pq, err := writeParquet([]timeRawINT32Row{{ID: 1, TimeVal: milliOfDay}})
		if err != nil {
			t.Fatalf("write parquet: %v", err)
		}
		loadErr := loadReader(ctx, client, dsID, tableID, pq, bigquery.Parquet, schema, false, 0, bigquery.WriteAppend)
		if loadErr != nil {
			t.Logf("[観測] INT32(annotなし) + 明示スキーマ TIME → 失敗: %v", loadErr)
			return
		}
		got := columnType(t, ctx, client, dsID, tableID, "time_val")
		rows, _ := queryRows(ctx, client, fmt.Sprintf("SELECT time_val FROM `%s`", fqTable(client, dsID, tableID)))
		t.Logf("[観測] INT32(annotなし) + 明示スキーマ TIME → 成功するが値がズレる: 型=%s, 値=%v (INT32=%d を「ミリ秒」のつもりが micros 解釈され 1000倍ズレ)", got, rows[0][0], milliOfDay)
	})
}
