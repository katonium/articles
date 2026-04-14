// Package dataflow_gcs_to_bq_test は Dataflow GCS->BQ 検証ワークスペースの
// Go テストスイート。
//
// 各 Beam YAML パイプラインは 3 つの変換 (カラム削除 + UTC→JST + NULL 行除去) を
// 1 本のチェーンとして順番に通し、最終結果を 1 つの BigQuery テーブルに書き込む。
//
// テストケース:
//   - 成功: csv / tsv / json / csv_gz / csv_python / csv_dated(A) / csv_dated(B) = 7
//   - 失敗: csv_required_fail (REQUIRED スキーマに NULLABLE データを書いてエラー) = 1
//   - 合計: 8
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
	"testing"
	"time"

	"cloud.google.com/go/bigquery"
	"cloud.google.com/go/storage"
	"google.golang.org/api/dataflow/v1b3"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
)

// ──────────────────────────────────────────────
// 期待値
// ──────────────────────────────────────────────

const tsLayout = "2006-01-02 15:04:05"

func mustParseUTC(s string) time.Time {
	t, err := time.Parse(tsLayout, s)
	if err != nil {
		panic(err)
	}
	return t.UTC()
}

func plus9h(t time.Time) time.Time {
	return t.Add(9 * time.Hour)
}

func tsStr(ts time.Time) string {
	return ts.UTC().Format(tsLayout)
}

// 全パイプライン共通の入力データ (UTC)
var inputUTC = map[int]time.Time{
	1: mustParseUTC("2026-04-10 01:00:00"),
	2: mustParseUTC("2026-04-10 15:30:00"),
	3: mustParseUTC("2026-04-10 10:00:00"),
	4: mustParseUTC("2026-04-10 23:00:00"),
	5: mustParseUTC("2026-04-10 05:00:00"),
}

// 全変換チェーン後の期待値: 3 行 (name が非 NULL の id=1,2,4)
// カラム: id, name, event_at_jst (secret は削除済、event_at_utc は JST 変換済)
var wantCleansed = [][]string{
	{"1", "Alice", tsStr(plus9h(inputUTC[1]))}, // 2026-04-10 10:00:00
	{"2", "Bob", tsStr(plus9h(inputUTC[2]))},   // 2026-04-11 00:30:00
	{"4", "Dave", tsStr(plus9h(inputUTC[4]))},   // 2026-04-11 08:00:00
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
	YamlPipelineGCSPaths  map[string]string
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
	}
}

// ──────────────────────────────────────────────
// フィクスチャアップロード
// ──────────────────────────────────────────────

func uploadFixtures(t *testing.T, ctx context.Context, tf *tfOutput) {
	t.Helper()

	client, err := storage.NewClient(ctx)
	if err != nil {
		t.Fatalf("storage client: %v", err)
	}
	defer client.Close()

	bucket := client.Bucket(tf.BucketName)

	uploadPlain := func(object, localPath string) {
		f, err := os.Open(localPath)
		if err != nil {
			t.Fatalf("open %s: %v", localPath, err)
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

	uploadGzipped := func(object, localPath string) {
		f, err := os.Open(localPath)
		if err != nil {
			t.Fatalf("open %s: %v", localPath, err)
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

	csvPath := filepath.Join("fixtures", "sample.csv")
	uploadPlain("input/sample.csv", csvPath)
	uploadPlain("input/sample.tsv", filepath.Join("fixtures", "sample.tsv"))
	uploadPlain("input/sample.json", filepath.Join("fixtures", "sample.json"))
	uploadGzipped("input/sample.csv.gz", csvPath)
}

func uploadDatedSample(t *testing.T, ctx context.Context, tf *tfOutput, today string) {
	t.Helper()
	client, err := storage.NewClient(ctx)
	if err != nil {
		t.Fatalf("storage client: %v", err)
	}
	defer client.Close()
	object := fmt.Sprintf("input/sample_%s.csv", today)
	f, err := os.Open(filepath.Join("fixtures", "sample.csv"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()
	w := client.Bucket(tf.BucketName).Object(object).NewWriter(ctx)
	if _, err := io.Copy(w, f); err != nil {
		_ = w.Close()
		t.Fatalf("upload: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	t.Logf("uploaded gs://%s/%s", tf.BucketName, object)
}

func deleteDatedSample(t *testing.T, ctx context.Context, tf *tfOutput, today string) {
	t.Helper()
	client, err := storage.NewClient(ctx)
	if err != nil {
		t.Logf("storage client (cleanup): %v", err)
		return
	}
	defer client.Close()
	_ = client.Bucket(tf.BucketName).Object(fmt.Sprintf("input/sample_%s.csv", today)).Delete(ctx)
}

// ──────────────────────────────────────────────
// テーブル管理 (Go 側で create / drop)
// ──────────────────────────────────────────────

// 全変換チェーン後の共通出力スキーマ: { id, name, event_at_jst }
var schemaResult = bigquery.Schema{
	{Name: "id", Type: bigquery.IntegerFieldType, Required: true},
	{Name: "name", Type: bigquery.StringFieldType, Required: false},
	{Name: "event_at_jst", Type: bigquery.TimestampFieldType, Required: false},
}

// 失敗テスト用: REQUIRED フィールドを含むスキーマ (Beam YAML の NULLABLE と衝突する)
var schemaRequiredFail = bigquery.Schema{
	{Name: "id", Type: bigquery.IntegerFieldType, Required: true},
	{Name: "name", Type: bigquery.StringFieldType, Required: true},      // ← REQUIRED
	{Name: "secret", Type: bigquery.StringFieldType, Required: true},    // ← REQUIRED
	{Name: "event_at_utc", Type: bigquery.TimestampFieldType, Required: true}, // ← REQUIRED
}

var staticFormats = []string{"csv", "tsv", "json", "csv_gz", "csv_python"}

// allTableSpecs は今回の test で必要な全テーブルの table_id → schema を返す。
func allTableSpecs(today string) map[string]bigquery.Schema {
	specs := make(map[string]bigquery.Schema)
	for _, fmtName := range staticFormats {
		specs[fmtName+"_result"] = schemaResult
	}
	specs[fmt.Sprintf("csv_dated_%s_result", today)] = schemaResult
	specs["csv_required_fail_result"] = schemaRequiredFail
	return specs
}

func createAllTables(t *testing.T, ctx context.Context, client *bigquery.Client, datasetID, today string) {
	t.Helper()
	for tableID, schema := range allTableSpecs(today) {
		table := client.Dataset(datasetID).Table(tableID)
		if err := table.Create(ctx, &bigquery.TableMetadata{Schema: schema}); err != nil {
			if strings.Contains(err.Error(), "Already Exists") || strings.Contains(err.Error(), "duplicate") {
				t.Logf("table %s already exists, reusing", tableID)
				continue
			}
			t.Fatalf("create table %s: %v", tableID, err)
		}
	}
	t.Logf("created %d tables", len(allTableSpecs(today)))
}

func dropAllTables(t *testing.T, ctx context.Context, client *bigquery.Client, datasetID, today string) {
	t.Helper()
	for tableID := range allTableSpecs(today) {
		_ = client.Dataset(datasetID).Table(tableID).Delete(ctx)
	}
}

func truncateTable(t *testing.T, ctx context.Context, client *bigquery.Client, projectID, datasetID, tableID string) {
	t.Helper()
	sql := fmt.Sprintf("TRUNCATE TABLE `%s.%s.%s`", projectID, datasetID, tableID)
	q := client.Query(sql)
	job, err := q.Run(ctx)
	if err != nil {
		t.Fatalf("truncate %s: %v", tableID, err)
	}
	if status, err := job.Wait(ctx); err != nil {
		t.Fatalf("truncate wait %s: %v", tableID, err)
	} else if err := status.Err(); err != nil {
		t.Fatalf("truncate status %s: %v", tableID, err)
	}
}

// ──────────────────────────────────────────────
// gcloud dataflow yaml run
// ──────────────────────────────────────────────

// gcloud dataflow yaml run --format=json は {"job":{"id":"...",...}} を返す
type gcloudJobOutput struct {
	Job struct {
		ID string `json:"id"`
	} `json:"job"`
}

type launchOpts struct {
	today        string
	passYyyymmdd bool
}

var formatToInputObject = map[string]string{
	"csv":               "input/sample.csv",
	"tsv":               "input/sample.tsv",
	"json":              "input/sample.json",
	"csv_gz":            "input/sample.csv.gz",
	"csv_python":        "input/sample.csv",
	"csv_required_fail": "input/sample.csv",
}

func launchYamlPipeline(t *testing.T, tf *tfOutput, format string, opts launchOpts) (string, error) {
	t.Helper()

	yamlPath, ok := tf.YamlPipelineGCSPaths[format]
	if !ok {
		return "", fmt.Errorf("yaml path not found for format %s", format)
	}

	jinjaVars := map[string]string{
		"project": tf.ProjectID,
		"dataset": tf.DatasetID,
		"prefix":  format,
	}

	if format == "csv_dated" {
		jinjaVars["input_path_prefix"] = fmt.Sprintf("gs://%s/input", tf.BucketName)
		if opts.passYyyymmdd {
			jinjaVars["yyyymmdd"] = opts.today
		}
	} else {
		objectName, ok := formatToInputObject[format]
		if !ok {
			return "", fmt.Errorf("input object not found for format %s", format)
		}
		jinjaVars["input_path"] = fmt.Sprintf("gs://%s/%s", tf.BucketName, objectName)
	}

	jinjaJSON, err := json.Marshal(jinjaVars)
	if err != nil {
		return "", fmt.Errorf("marshal jinja: %w", err)
	}

	jobNameSuffix := strings.ReplaceAll(format, "_", "-")
	if format == "csv_dated" {
		if opts.passYyyymmdd {
			jobNameSuffix = "csv-dated-a"
		} else {
			jobNameSuffix = "csv-dated-b"
		}
	}
	jobName := fmt.Sprintf("df-gcs-to-bq-%s-%d", jobNameSuffix, time.Now().Unix())

	args := []string{
		"dataflow", "yaml", "run", jobName,
		"--yaml-pipeline-file", yamlPath,
		"--region", tf.Region,
		"--service-account-email", tf.DataflowWorkerSAEmail,
		"--temp-location", fmt.Sprintf("gs://%s/temp/", tf.BucketName),
		"--staging-location", fmt.Sprintf("gs://%s/staging/", tf.BucketName),
		"--jinja-variables", string(jinjaJSON),
		"--num-workers", "1",
		"--max-workers", "1",
		"--worker-machine-type", "e2-standard-2",
		"--project", tf.ProjectID,
		"--format", "json",
	}
	cmd := exec.Command("gcloud", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("gcloud yaml run failed: %w\nstderr: %s", err, stderr.String())
	}

	var out gcloudJobOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		return "", fmt.Errorf("parse: %w\nstdout: %s", err, stdout.String())
	}
	if out.Job.ID == "" {
		return "", fmt.Errorf("no job id\nstdout: %s", stdout.String())
	}
	return out.Job.ID, nil
}

// ──────────────────────────────────────────────
// Dataflow ジョブ完了待機
// ──────────────────────────────────────────────

// waitForJob は Dataflow ジョブの完了を待つ。
// タイムアウト時は自動で cancel リクエストを送り、課金が暴走するのを防ぐ。
func waitForJob(ctx context.Context, projectID, region, jobID string) (string, error) {
	// ADC の quota project が対象プロジェクトと違う場合、Dataflow API 呼び出しが
	// 「API not enabled」で失敗する。WithQuotaProject で明示的に指定する。
	svc, err := dataflow.NewService(ctx, option.WithQuotaProject(projectID))
	if err != nil {
		return "", err
	}
	const (
		pollInterval = 15 * time.Second
		timeout      = 20 * time.Minute
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

	// タイムアウト: 課金防止のためジョブを cancel する
	_, cancelErr := svc.Projects.Locations.Jobs.Update(
		projectID, region, jobID,
		&dataflow.Job{RequestedState: "JOB_STATE_CANCELLED"},
	).Do()
	if cancelErr != nil {
		return "", fmt.Errorf("job %s timeout after %s, cancel also failed: %v", jobID, timeout, cancelErr)
	}
	return "JOB_STATE_CANCELLED", fmt.Errorf("job %s timeout after %s (auto-cancelled to prevent cost runaway)", jobID, timeout)
}

// ──────────────────────────────────────────────
// BigQuery クエリ + 正規化比較
// ──────────────────────────────────────────────

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

// assertCleansedResult は全変換チェーン後のテーブルを検証する。
// 期待: 3 行 (id ∈ {1,2,4}), カラム { id, name, event_at_jst }
func assertCleansedResult(t *testing.T, ctx context.Context, client *bigquery.Client, fqTable string) {
	t.Helper()
	sql := fmt.Sprintf("SELECT id, name, event_at_jst FROM `%s` ORDER BY id", fqTable)
	rows, err := queryRows(ctx, client, sql)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	got := normalizeAll(rows)
	if !equalRows(wantCleansed, got) {
		t.Fatalf("mismatch:\nwant:\n%sgot:\n%s", dumpRows(wantCleansed), dumpRows(got))
	}
}

// ──────────────────────────────────────────────
// メインのテストケース
// ──────────────────────────────────────────────

// allFormats は並列起動する成功パイプラインの format 識別子。
// csv_dated は Mode A (明示パラメータ) として参加する。
// csv_required_fail は失敗を期待するので別扱い。
var allFormats = []string{"csv", "tsv", "json", "csv_gz", "csv_python", "csv_dated"}

func TestDataflowGcsToBq(t *testing.T) {
	ctx := context.Background()
	tf := getTerraformOutputs(t)

	bqClient, err := bigquery.NewClient(ctx, tf.ProjectID)
	if err != nil {
		t.Fatalf("bq client: %v", err)
	}
	defer bqClient.Close()

	today := time.Now().UTC().Format("20060102")
	t.Logf("test date (UTC): %s", today)

	// ── Setup ──
	uploadFixtures(t, ctx, tf)
	uploadDatedSample(t, ctx, tf, today)
	createAllTables(t, ctx, bqClient, tf.DatasetID, today)
	defer func() {
		if !t.Failed() {
			dropAllTables(t, ctx, bqClient, tf.DatasetID, today)
		} else {
			t.Logf("test failed: leaving tables for debugging")
		}
		deleteDatedSample(t, ctx, tf, today)
	}()

	// ── 成功パイプライン 6 本を逐次起動 ──
	//
	// 並列起動だと launcher VM + worker VM の IP アドレスが IN_USE_ADDRESSES quota
	// (us-central1 で 8) を超過する。1 ジョブずつ起動→完了→次、にすることで
	// 最大 2 VM (launcher + worker) に抑える。合計 ~35 分かかるが確実に通る。
	for _, f := range allFormats {
		format := f
		opts := launchOpts{}
		if format == "csv_dated" {
			opts.today = today
			opts.passYyyymmdd = true
		}
		t.Run("run/"+format, func(t *testing.T) {
			id, err := launchYamlPipeline(t, tf, format, opts)
			if err != nil {
				t.Fatalf("launch %s: %v", format, err)
			}
			t.Logf("launched %s: id=%s", format, id)

			state, err := waitForJob(ctx, tf.ProjectID, tf.Region, id)
			if err != nil {
				t.Fatalf("%s (%s): %v", format, id, err)
			}
			t.Logf("job %s (%s) ended in state=%s", format, id, state)
		})
		if t.Failed() {
			t.FailNow()
		}
	}

	// ── 失敗パイプライン 1 本を起動 ──
	failJobID, err := launchYamlPipeline(t, tf, "csv_required_fail", launchOpts{})
	if err != nil {
		t.Fatalf("launch csv_required_fail: %v", err)
	}
	t.Logf("launched csv_required_fail: id=%s", failJobID)

	// ── 成功パイプラインのアサーション (6 ケース) ──
	resultTables := []string{
		"csv_result", "tsv_result", "json_result",
		"csv_gz_result", "csv_python_result",
		fmt.Sprintf("csv_dated_%s_result", today),
	}
	sort.Strings(resultTables)

	for _, tableID := range resultTables {
		tableID := tableID
		fqTable := fmt.Sprintf("%s.%s.%s", tf.ProjectID, tf.DatasetID, tableID)
		t.Run("modeA/"+tableID, func(t *testing.T) {
			assertCleansedResult(t, ctx, bqClient, fqTable)
		})
	}

	if t.Failed() {
		return
	}

	// ── csv_dated Mode B (jinja 変数なし) ──
	t.Run("modeB/csv_dated", func(t *testing.T) {
		datedTableID := fmt.Sprintf("csv_dated_%s_result", today)
		truncateTable(t, ctx, bqClient, tf.ProjectID, tf.DatasetID, datedTableID)

		jobID, err := launchYamlPipeline(t, tf, "csv_dated", launchOpts{
			today:        today,
			passYyyymmdd: false,
		})
		if err != nil {
			t.Fatalf("launch Mode B: %v", err)
		}
		t.Logf("launched csv_dated Mode B: id=%s", jobID)

		state, err := waitForJob(ctx, tf.ProjectID, tf.Region, jobID)
		if err != nil {
			t.Fatalf("wait Mode B (%s): %v", jobID, err)
		}
		t.Logf("csv_dated Mode B ended in state=%s", state)

		fqTable := fmt.Sprintf("%s.%s.%s", tf.ProjectID, tf.DatasetID, datedTableID)
		assertCleansedResult(t, ctx, bqClient, fqTable)
	})

	// ── 失敗パイプラインのアサーション: JOB_STATE_FAILED を期待 ──
	t.Run("expected_failure/csv_required_fail", func(t *testing.T) {
		state, err := waitForJob(ctx, tf.ProjectID, tf.Region, failJobID)
		if err == nil {
			t.Fatalf("expected csv_required_fail to FAIL but got state=%s", state)
		}
		if state != "JOB_STATE_FAILED" {
			t.Fatalf("expected JOB_STATE_FAILED but got state=%s, err=%v", state, err)
		}
		t.Logf("csv_required_fail correctly ended in JOB_STATE_FAILED: %v", err)
	})
}
