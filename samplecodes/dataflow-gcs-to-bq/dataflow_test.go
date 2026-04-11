// Package dataflow_gcs_to_bq_test は Dataflow GCS->BQ 検証ワークスペースの
// Go テストスイート。
//
// 責務:
//   - フィクスチャ (CSV/TSV/JSON/csv.gz) を GCS にアップロード
//   - 各テストの前後で宛先テーブルを TRUNCATE
//   - `gcloud dataflow yaml run` で 5 本の Beam YAML パイプラインを並列起動
//   - Dataflow REST API で JOB_STATE_DONE を待機
//   - BigQuery にクエリして 15 ケース (5 形式 × 3 変換) をアサーション
//
// 検証対象 (Beam YAML パイプライン) には一切のテスト用ロジックを入れず、
// 「Go テストが通る = YAML が宣言通りに動いている」という保証になるよう書く。
package dataflow_gcs_to_bq_test

import (
	"bytes"
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

// ──────────────────────────────────────────────
// terraform output 取得
// ──────────────────────────────────────────────

type tfOutput struct {
	ProjectID             string
	Region                string
	BucketName            string
	DatasetID             string
	DataflowWorkerSAEmail string
	YamlPipelineGCSPaths  map[string]string // key: format -> gs://.../pipelines/{format}.yaml
	DestinationTables     map[string]string // key: "{format}_{pattern}" -> table_id
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
		YamlPipelineGCSPaths:  getMap("yaml_pipeline_gcs_paths"),
		DestinationTables:     getMap("destination_tables"),
	}
}

// ──────────────────────────────────────────────
// フィクスチャアップロード
// ──────────────────────────────────────────────

const (
	gcsObjectCSV   = "input/sample.csv"
	gcsObjectTSV   = "input/sample.tsv"
	gcsObjectJSON  = "input/sample.json"
	gcsObjectCSVGZ = "input/sample.csv.gz"
)

// 各 format に対応する GCS 入力ファイル。csv_python は csv と同じファイルを使う。
var formatToInputObject = map[string]string{
	"csv":        gcsObjectCSV,
	"tsv":        gcsObjectTSV,
	"json":       gcsObjectJSON,
	"csv_gz":     gcsObjectCSVGZ,
	"csv_python": gcsObjectCSV,
}

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
// gcloud dataflow yaml run でジョブを起動
// ──────────────────────────────────────────────

// gcloudJobOutput は `gcloud dataflow yaml run --format=json` の戻り値を受ける
// 構造体。最低限必要な job ID だけ拾う。
type gcloudJobOutput struct {
	ID string `json:"id"`
}

func launchYamlPipeline(t *testing.T, tf *tfOutput, format string) (string, error) {
	t.Helper()

	yamlPath, ok := tf.YamlPipelineGCSPaths[format]
	if !ok {
		return "", fmt.Errorf("yaml path not found for format %s", format)
	}

	objectName, ok := formatToInputObject[format]
	if !ok {
		return "", fmt.Errorf("input object not found for format %s", format)
	}
	inputPath := fmt.Sprintf("gs://%s/%s", tf.BucketName, objectName)

	jinjaVars := map[string]string{
		"input_path": inputPath,
		"project":    tf.ProjectID,
		"dataset":    tf.DatasetID,
		"prefix":     format,
	}
	jinjaJSON, err := json.Marshal(jinjaVars)
	if err != nil {
		return "", fmt.Errorf("marshal jinja: %w", err)
	}

	jobName := fmt.Sprintf("df-gcs-to-bq-%s-%d",
		strings.ReplaceAll(format, "_", "-"), time.Now().Unix())

	args := []string{
		"dataflow", "yaml", "run", jobName,
		"--yaml-pipeline-file", yamlPath,
		"--region", tf.Region,
		"--service-account-email", tf.DataflowWorkerSAEmail,
		"--temp-location", fmt.Sprintf("gs://%s/temp/", tf.BucketName),
		"--staging-location", fmt.Sprintf("gs://%s/staging/", tf.BucketName),
		"--jinja-variables", string(jinjaJSON),
		"--project", tf.ProjectID,
		"--format", "json",
	}
	cmd := exec.Command("gcloud", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("gcloud dataflow yaml run failed: %w\nstderr: %s",
			err, stderr.String())
	}

	var out gcloudJobOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		return "", fmt.Errorf("parse gcloud output: %w\nstdout: %s",
			err, stdout.String())
	}
	if out.ID == "" {
		return "", fmt.Errorf("gcloud output has no job id\nstdout: %s", stdout.String())
	}
	return out.ID, nil
}

// ──────────────────────────────────────────────
// Dataflow ジョブ完了待機
// ──────────────────────────────────────────────

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

func assertUtcJst(t *testing.T, ctx context.Context, client *bigquery.Client, fqTable string) {
	t.Helper()
	sql := fmt.Sprintf("SELECT id, name, secret, event_at_jst FROM `%s` ORDER BY id", fqTable)
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

func assertNullDrop(t *testing.T, ctx context.Context, client *bigquery.Client, fqTable string) {
	t.Helper()
	sql := fmt.Sprintf("SELECT id, name, secret, event_at_utc FROM `%s` ORDER BY id", fqTable)
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

var allFormats = []string{"csv", "tsv", "json", "csv_gz", "csv_python"}

func TestDataflowGcsToBq(t *testing.T) {
	ctx := context.Background()
	tf := getTerraformOutputs(t)

	// SetUp: フィクスチャアップロード + 全テーブル truncate
	uploadFixtures(t, ctx, tf)
	truncateAllTables(t, ctx, tf)

	// 5 ジョブを並列起動
	jobIDs := make(map[string]string, len(allFormats))
	var jobMu sync.Mutex
	var launchWg sync.WaitGroup
	var launchErrs []error

	for _, f := range allFormats {
		launchWg.Add(1)
		go func(format string) {
			defer launchWg.Done()
			id, err := launchYamlPipeline(t, tf, format)
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

	// 全ジョブの完了待機
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

	// 15 ケースをアサーション
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
		// key は "{format}_{pattern}" 形式
		format, pattern := splitFormatPattern(key)
		assertFn, ok := assertionByPattern[pattern]
		if !ok {
			t.Errorf("unknown pattern in table key: %s", key)
			continue
		}

		fqTable := fmt.Sprintf("%s.%s.%s", tf.ProjectID, tf.DatasetID, tf.DestinationTables[key])
		subname := fmt.Sprintf("format=%s/pattern=%s", format, pattern)
		t.Run(subname, func(t *testing.T) {
			assertFn(t, ctx, bqClient, fqTable)
		})
	}

	// TearDown: 後片付け (テストが通った場合のみ。失敗時はデバッグのため残す)
	if !t.Failed() {
		truncateAllTables(t, ctx, tf)
	}
}

// "csv_drop_col" -> ("csv", "drop_col")
// "csv_gz_utc_jst" -> ("csv_gz", "utc_jst")
// "csv_python_null_drop" -> ("csv_python", "null_drop")
func splitFormatPattern(key string) (format string, pattern string) {
	for _, p := range []string{"drop_col", "utc_jst", "null_drop"} {
		if strings.HasSuffix(key, "_"+p) {
			return strings.TrimSuffix(key, "_"+p), p
		}
	}
	return "", key
}
