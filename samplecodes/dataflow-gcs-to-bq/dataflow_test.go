// Package dataflow_gcs_to_bq_test は Dataflow GCS->BQ 検証ワークスペースの
// Go テストスイート。
//
// 責務:
//   - TestMain: フィクスチャ (CSV/TSV/JSON/csv.gz) を GCS にアップロード
//   - 各 subtest の前に宛先テーブルを TRUNCATE
//   - Dataflow REST API で Flex Template を 4 並列でキックし完了待機
//   - BigQuery にクエリして 24 ケースをアサーション
//   - 後片付け
//
// 検証対象 (Dataflow パイプライン) には一切のテスト用ロジックを入れず、
// 「Go テストが通る = Dataflow が期待通りに動いている」という保証になるよう書く。
package dataflow_gcs_to_bq_test

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"cloud.google.com/go/bigquery"
	"cloud.google.com/go/storage"
	"google.golang.org/api/dataflow/v1b3"
	"google.golang.org/api/iterator"
)

// ──────────────────────────────────────────────
// テストフィクスチャと期待値
// ──────────────────────────────────────────────

const tsLayout = "2006-01-02 15:04:05"

func mustParseUTC(s string) time.Time {
	t, err := time.Parse(tsLayout, s)
	if err != nil {
		panic(err)
	}
	return t.UTC()
}

// 全テスト共通の入力データ (UTC)
var inputUTC = map[int]time.Time{
	1: mustParseUTC("2026-04-10 01:00:00"),
	2: mustParseUTC("2026-04-10 15:30:00"),
	3: mustParseUTC("2026-04-10 10:00:00"),
	4: mustParseUTC("2026-04-10 23:00:00"),
	5: mustParseUTC("2026-04-10 05:00:00"),
}

func plus9h(t time.Time) time.Time {
	return t.Add(9 * time.Hour)
}

func nullString(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

// ──────────────────────────────────────────────
// terraform output 取得
// ──────────────────────────────────────────────

type tfOutput struct {
	ProjectID             string
	Region                string
	BucketName            string
	DatasetID             string
	DataflowWorkerSAEmail string
	TemplateSpecGCSPath   string
	DestinationTables     map[string]string // key: "{format}_{lang}_{pattern}"
}

func getTerraformOutputs(t *testing.T) *tfOutput {
	t.Helper()

	cmd := exec.Command("terraform", "output", "-json")
	cmd.Dir = "."
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("terraform output の取得に失敗: %v", err)
	}

	var raw map[string]struct {
		Value interface{} `json:"value"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		t.Fatalf("terraform output の解析に失敗: %v", err)
	}

	getString := func(key string) string {
		v, ok := raw[key]
		if !ok {
			t.Fatalf("terraform output に %s が見つかりません", key)
		}
		s, ok := v.Value.(string)
		if !ok {
			t.Fatalf("terraform output %s が文字列ではありません", key)
		}
		return s
	}

	getMap := func(key string) map[string]string {
		v, ok := raw[key]
		if !ok {
			t.Fatalf("terraform output に %s が見つかりません", key)
		}
		m, ok := v.Value.(map[string]interface{})
		if !ok {
			t.Fatalf("terraform output %s が map ではありません", key)
		}
		result := make(map[string]string, len(m))
		for k, v := range m {
			s, ok := v.(string)
			if !ok {
				t.Fatalf("terraform output %s.%s が string ではありません", key, k)
			}
			result[k] = s
		}
		return result
	}

	return &tfOutput{
		ProjectID:             getString("project_id"),
		Region:                getString("region"),
		BucketName:            getString("bucket_name"),
		DatasetID:             getString("dataset_id"),
		DataflowWorkerSAEmail: getString("dataflow_worker_sa_email"),
		TemplateSpecGCSPath:   getString("template_spec_gcs_path"),
		DestinationTables:     getMap("destination_tables"),
	}
}

// ──────────────────────────────────────────────
// フィクスチャアップロード
// ──────────────────────────────────────────────

const (
	gcsObjectCSV    = "input/sample.csv"
	gcsObjectTSV    = "input/sample.tsv"
	gcsObjectJSON   = "input/sample.json"
	gcsObjectCSVGZ  = "input/sample.csv.gz"
)

func uploadFixtures(t *testing.T, ctx context.Context, tf *tfOutput) {
	t.Helper()

	client, err := storage.NewClient(ctx)
	if err != nil {
		t.Fatalf("storage client: %v", err)
	}
	defer client.Close()

	bucket := client.Bucket(tf.BucketName)

	uploadPlain := func(object, path string) {
		f, err := os.Open(path)
		if err != nil {
			t.Fatalf("open %s: %v", path, err)
		}
		defer f.Close()

		w := bucket.Object(object).NewWriter(ctx)
		if _, err := io.Copy(w, f); err != nil {
			_ = w.Close()
			t.Fatalf("upload %s: %v", object, err)
		}
		if err := w.Close(); err != nil {
			t.Fatalf("close writer %s: %v", object, err)
		}
		t.Logf("uploaded gs://%s/%s", tf.BucketName, object)
	}

	uploadGzipped := func(object, path string) {
		f, err := os.Open(path)
		if err != nil {
			t.Fatalf("open %s: %v", path, err)
		}
		defer f.Close()

		w := bucket.Object(object).NewWriter(ctx)
		gw := gzip.NewWriter(w)
		if _, err := io.Copy(gw, f); err != nil {
			_ = gw.Close()
			_ = w.Close()
			t.Fatalf("gzip upload %s: %v", object, err)
		}
		if err := gw.Close(); err != nil {
			_ = w.Close()
			t.Fatalf("gzip close %s: %v", object, err)
		}
		if err := w.Close(); err != nil {
			t.Fatalf("close writer %s: %v", object, err)
		}
		t.Logf("uploaded gs://%s/%s (gzipped)", tf.BucketName, object)
	}

	uploadPlain(gcsObjectCSV, filepath.Join("fixtures", "sample.csv"))
	uploadPlain(gcsObjectTSV, filepath.Join("fixtures", "sample.tsv"))
	uploadPlain(gcsObjectJSON, filepath.Join("fixtures", "sample.json"))
	// gzip は Go 側でアーカイブして送ることでフィクスチャをテキストで管理する
	uploadGzipped(gcsObjectCSVGZ, filepath.Join("fixtures", "sample.csv"))
}

// ──────────────────────────────────────────────
// 宛先テーブル truncate
// ──────────────────────────────────────────────

func truncateAllTables(t *testing.T, ctx context.Context, tf *tfOutput) {
	t.Helper()

	client, err := bigquery.NewClient(ctx, tf.ProjectID)
	if err != nil {
		t.Fatalf("bq client: %v", err)
	}
	defer client.Close()

	var wg sync.WaitGroup
	var mu sync.Mutex
	var errs []error

	for _, tbl := range tf.DestinationTables {
		wg.Add(1)
		go func(table string) {
			defer wg.Done()
			sql := fmt.Sprintf("TRUNCATE TABLE `%s.%s.%s`", tf.ProjectID, tf.DatasetID, table)
			q := client.Query(sql)
			job, err := q.Run(ctx)
			if err != nil {
				mu.Lock()
				errs = append(errs, fmt.Errorf("truncate %s: %w", table, err))
				mu.Unlock()
				return
			}
			status, err := job.Wait(ctx)
			if err != nil {
				mu.Lock()
				errs = append(errs, fmt.Errorf("truncate wait %s: %w", table, err))
				mu.Unlock()
				return
			}
			if err := status.Err(); err != nil {
				mu.Lock()
				errs = append(errs, fmt.Errorf("truncate status %s: %w", table, err))
				mu.Unlock()
				return
			}
		}(tbl)
	}
	wg.Wait()

	for _, err := range errs {
		t.Errorf("%v", err)
	}
	if len(errs) > 0 {
		t.FailNow()
	}
}

// ──────────────────────────────────────────────
// Dataflow Flex Template 起動 / 完了待機
// ──────────────────────────────────────────────

var formatToInputObject = map[string]string{
	"csv":    gcsObjectCSV,
	"tsv":    gcsObjectTSV,
	"json":   gcsObjectJSON,
	"csv_gz": gcsObjectCSVGZ,
}

func launchFlexTemplate(ctx context.Context, tf *tfOutput, format string) (string, error) {
	svc, err := dataflow.NewService(ctx)
	if err != nil {
		return "", fmt.Errorf("dataflow service: %w", err)
	}

	objectName, ok := formatToInputObject[format]
	if !ok {
		return "", fmt.Errorf("unknown format: %s", format)
	}
	inputPath := fmt.Sprintf("gs://%s/%s", tf.BucketName, objectName)

	jobName := fmt.Sprintf("df-gcs-to-bq-%s-%d", strings.ReplaceAll(format, "_", "-"), time.Now().Unix())

	req := &dataflow.LaunchFlexTemplateRequest{
		LaunchParameter: &dataflow.LaunchFlexTemplateParameter{
			JobName:              jobName,
			ContainerSpecGcsPath: tf.TemplateSpecGCSPath,
			Parameters: map[string]string{
				"format":              format,
				"input_path":          inputPath,
				"output_table_prefix": format,
				"bq_project":          tf.ProjectID,
				"bq_dataset":          tf.DatasetID,
			},
			Environment: &dataflow.FlexTemplateRuntimeEnvironment{
				ServiceAccountEmail: tf.DataflowWorkerSAEmail,
				TempLocation:        fmt.Sprintf("gs://%s/temp/", tf.BucketName),
				StagingLocation:     fmt.Sprintf("gs://%s/staging/", tf.BucketName),
			},
		},
	}

	resp, err := svc.Projects.Locations.FlexTemplates.Launch(tf.ProjectID, tf.Region, req).Do()
	if err != nil {
		return "", fmt.Errorf("launch flex template: %w", err)
	}
	if resp.Job == nil {
		return "", fmt.Errorf("launch response has no job")
	}
	return resp.Job.Id, nil
}

func waitForJob(ctx context.Context, projectID, region, jobID string) (string, error) {
	svc, err := dataflow.NewService(ctx)
	if err != nil {
		return "", err
	}

	const (
		pollInterval = 15 * time.Second
		timeout      = 25 * time.Minute
	)

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		job, err := svc.Projects.Locations.Jobs.Get(projectID, region, jobID).Do()
		if err != nil {
			return "", fmt.Errorf("get job: %w", err)
		}
		switch job.CurrentState {
		case "JOB_STATE_DONE":
			return job.CurrentState, nil
		case "JOB_STATE_FAILED", "JOB_STATE_CANCELLED", "JOB_STATE_DRAINED", "JOB_STATE_UPDATED":
			return job.CurrentState, fmt.Errorf("job %s terminated in state %s", jobID, job.CurrentState)
		}
		time.Sleep(pollInterval)
	}
	return "", fmt.Errorf("job %s did not finish within %s", jobID, timeout)
}

// ──────────────────────────────────────────────
// BigQuery 結果取得 + 正規化比較
// ──────────────────────────────────────────────

// queryRows は SELECT クエリを実行し、行を [][]bigquery.Value で返す
func queryRows(ctx context.Context, client *bigquery.Client, sql string) ([][]bigquery.Value, error) {
	q := client.Query(sql)
	it, err := q.Read(ctx)
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

// normalize は []bigquery.Value を比較しやすい文字列スライスに変換する
func normalize(row []bigquery.Value) []string {
	out := make([]string, len(row))
	for i, v := range row {
		if v == nil {
			out[i] = "<nil>"
			continue
		}
		switch x := v.(type) {
		case time.Time:
			out[i] = x.UTC().Format(tsLayout)
		case int64:
			out[i] = fmt.Sprintf("%d", x)
		case string:
			out[i] = x
		default:
			out[i] = fmt.Sprintf("%v", x)
		}
	}
	return out
}

func normalizeAll(rows [][]bigquery.Value) [][]string {
	out := make([][]string, len(rows))
	for i, r := range rows {
		out[i] = normalize(r)
	}
	return out
}

func equalRows(want, got [][]string) bool {
	if len(want) != len(got) {
		return false
	}
	for i := range want {
		if len(want[i]) != len(got[i]) {
			return false
		}
		for j := range want[i] {
			if want[i][j] != got[i][j] {
				return false
			}
		}
	}
	return true
}

func dumpRows(rows [][]string) string {
	var sb strings.Builder
	for _, r := range rows {
		sb.WriteString("  [")
		sb.WriteString(strings.Join(r, ", "))
		sb.WriteString("]\n")
	}
	return sb.String()
}

// ──────────────────────────────────────────────
// アサーション (3 種類の変換パターン)
// ──────────────────────────────────────────────

type assertion func(t *testing.T, ctx context.Context, client *bigquery.Client, fqTable string)

func tsStr(ts time.Time) string {
	return ts.UTC().Format(tsLayout)
}

// drop_col の期待値: secret 列を削除した 5 行
func assertDropCol(t *testing.T, ctx context.Context, client *bigquery.Client, fqTable string) {
	t.Helper()
	sql := fmt.Sprintf("SELECT id, name, event_at_utc FROM `%s` ORDER BY id", fqTable)
	rows, err := queryRows(ctx, client, sql)
	if err != nil {
		t.Fatalf("query: %v", err)
	}

	want := [][]string{
		{"1", "Alice", tsStr(inputUTC[1])},
		{"2", "Bob", tsStr(inputUTC[2])},
		{"3", "<nil>", tsStr(inputUTC[3])},
		{"4", "Dave", tsStr(inputUTC[4])},
		{"5", "<nil>", tsStr(inputUTC[5])},
	}
	got := normalizeAll(rows)
	if !equalRows(want, got) {
		t.Fatalf("drop_col mismatch:\nwant:\n%sgot:\n%s", dumpRows(want), dumpRows(got))
	}
}

// utc_jst の期待値: event_at_jst が UTC+9 になった 5 行
func assertUtcJst(t *testing.T, ctx context.Context, client *bigquery.Client, fqTable string) {
	t.Helper()
	sql := fmt.Sprintf(
		"SELECT id, name, secret, event_at_jst FROM `%s` ORDER BY id",
		fqTable,
	)
	rows, err := queryRows(ctx, client, sql)
	if err != nil {
		t.Fatalf("query: %v", err)
	}

	want := [][]string{
		{"1", "Alice", "foo", tsStr(plus9h(inputUTC[1]))},
		{"2", "Bob", "bar", tsStr(plus9h(inputUTC[2]))},
		{"3", "<nil>", "baz", tsStr(plus9h(inputUTC[3]))},
		{"4", "Dave", "qux", tsStr(plus9h(inputUTC[4]))},
		{"5", "<nil>", "quux", tsStr(plus9h(inputUTC[5]))},
	}
	got := normalizeAll(rows)
	if !equalRows(want, got) {
		t.Fatalf("utc_jst mismatch:\nwant:\n%sgot:\n%s", dumpRows(want), dumpRows(got))
	}
}

// null_drop の期待値: name が NULL でない 3 行 (id ∈ {1, 2, 4})
func assertNullDrop(t *testing.T, ctx context.Context, client *bigquery.Client, fqTable string) {
	t.Helper()
	sql := fmt.Sprintf(
		"SELECT id, name, secret, event_at_utc FROM `%s` ORDER BY id",
		fqTable,
	)
	rows, err := queryRows(ctx, client, sql)
	if err != nil {
		t.Fatalf("query: %v", err)
	}

	want := [][]string{
		{"1", "Alice", "foo", tsStr(inputUTC[1])},
		{"2", "Bob", "bar", tsStr(inputUTC[2])},
		{"4", "Dave", "qux", tsStr(inputUTC[4])},
	}
	got := normalizeAll(rows)
	if !equalRows(want, got) {
		t.Fatalf("null_drop mismatch:\nwant:\n%sgot:\n%s", dumpRows(want), dumpRows(got))
	}
}

var assertionByPattern = map[string]assertion{
	"drop_col":  assertDropCol,
	"utc_jst":   assertUtcJst,
	"null_drop": assertNullDrop,
}

// ──────────────────────────────────────────────
// メインのテストケース
// ──────────────────────────────────────────────

func TestDataflowGcsToBq(t *testing.T) {
	ctx := context.Background()
	tf := getTerraformOutputs(t)

	// SetUp: フィクスチャアップロード + 全テーブル truncate
	uploadFixtures(t, ctx, tf)
	truncateAllTables(t, ctx, tf)

	// 4 ジョブを並列起動 → 全完了待ち
	formats := []string{"csv", "tsv", "json", "csv_gz"}
	jobIDs := make(map[string]string, len(formats))
	var jobMu sync.Mutex

	var launchWg sync.WaitGroup
	var launchErrs []error
	for _, f := range formats {
		launchWg.Add(1)
		go func(format string) {
			defer launchWg.Done()
			id, err := launchFlexTemplate(ctx, tf, format)
			jobMu.Lock()
			defer jobMu.Unlock()
			if err != nil {
				launchErrs = append(launchErrs, fmt.Errorf("launch %s: %w", format, err))
				return
			}
			jobIDs[format] = id
			t.Logf("launched %s job: id=%s", format, id)
		}(f)
	}
	launchWg.Wait()

	for _, err := range launchErrs {
		t.Errorf("%v", err)
	}
	if t.Failed() {
		t.FailNow()
	}

	var waitWg sync.WaitGroup
	var waitMu sync.Mutex
	var waitErrs []error
	for f, id := range jobIDs {
		waitWg.Add(1)
		go func(format, jobID string) {
			defer waitWg.Done()
			state, err := waitForJob(ctx, tf.ProjectID, tf.Region, jobID)
			waitMu.Lock()
			defer waitMu.Unlock()
			if err != nil {
				waitErrs = append(waitErrs, fmt.Errorf("wait %s (%s): %w", format, jobID, err))
				return
			}
			t.Logf("job %s for format=%s ended in state=%s", jobID, format, state)
		}(f, id)
	}
	waitWg.Wait()

	for _, err := range waitErrs {
		t.Errorf("%v", err)
	}
	if t.Failed() {
		t.FailNow()
	}

	// 24 ケースをアサーション
	bqClient, err := bigquery.NewClient(ctx, tf.ProjectID)
	if err != nil {
		t.Fatalf("bq client: %v", err)
	}
	defer bqClient.Close()

	keys := make([]string, 0, len(tf.DestinationTables))
	for k := range tf.DestinationTables {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, key := range keys {
		key := key
		// key は "{format}_{lang}_{pattern}" 形式
		// format は csv / tsv / json / csv_gz, lang は sql / py,
		// pattern は drop_col / utc_jst / null_drop
		var format, lang, pattern string
		switch {
		case strings.HasPrefix(key, "csv_gz_"):
			format = "csv_gz"
			rest := strings.TrimPrefix(key, "csv_gz_")
			lang, pattern = splitLangPattern(rest)
		default:
			// csv_, tsv_, json_
			i := strings.Index(key, "_")
			format = key[:i]
			rest := key[i+1:]
			lang, pattern = splitLangPattern(rest)
		}

		assertFn, ok := assertionByPattern[pattern]
		if !ok {
			t.Errorf("unknown pattern in table key: %s", key)
			continue
		}

		fqTable := fmt.Sprintf("%s.%s.%s", tf.ProjectID, tf.DatasetID, tf.DestinationTables[key])
		subname := fmt.Sprintf("format=%s/lang=%s/pattern=%s", format, lang, pattern)
		t.Run(subname, func(t *testing.T) {
			assertFn(t, ctx, bqClient, fqTable)
		})
	}

	// TearDown: 後片付け (テストが通った場合のみ。失敗時はデバッグのため残す)
	if !t.Failed() {
		truncateAllTables(t, ctx, tf)
	}
}

// "sql_drop_col" -> ("sql", "drop_col")
// "py_utc_jst"   -> ("py", "utc_jst")
// "sql_null_drop" -> ("sql", "null_drop")
func splitLangPattern(s string) (lang string, pattern string) {
	switch {
	case strings.HasPrefix(s, "sql_"):
		return "sql", strings.TrimPrefix(s, "sql_")
	case strings.HasPrefix(s, "py_"):
		return "py", strings.TrimPrefix(s, "py_")
	}
	return "", s
}
