package cloudbuild_private_pool_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	cloudbuild "cloud.google.com/go/cloudbuild/apiv1/v2"
	cloudbuildpb "cloud.google.com/go/cloudbuild/apiv1/v2/cloudbuildpb"
	"google.golang.org/protobuf/types/known/durationpb"
)

// tfOutput は terraform output -json の結果を格納する構造体
type tfOutput struct {
	ProjectID    string
	Region       string
	Suffix       string
	WorkerPoolID string
	NetworkName  string
	SAEmailA     string
	SAEmailB     string
	BucketA      string
	BucketB      string
	ARRepoName   string
}

// getTerraformOutputs は terraform output からテスト用パラメータを取得する
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

	return &tfOutput{
		ProjectID:    getString("project_id"),
		Region:       getString("region"),
		Suffix:       getString("suffix"),
		WorkerPoolID: getString("worker_pool_id"),
		NetworkName:  getString("network_name"),
		SAEmailA:     getString("sa_a_email"),
		SAEmailB:     getString("sa_b_email"),
		BucketA:      getString("bucket_a_name"),
		BucketB:      getString("bucket_b_name"),
		ARRepoName:   getString("ar_repo_name"),
	}
}

// buildResult はビルド実行の結果を保持する
type buildResult struct {
	Status cloudbuildpb.Build_Status
	Logs   string
}

// submitBuild はプライベートプールにビルドを投入し、完了まで待機する
func submitBuild(t *testing.T, ctx context.Context, o *tfOutput, saEmail string, steps []*cloudbuildpb.BuildStep) *buildResult {
	t.Helper()

	client, err := cloudbuild.NewClient(ctx)
	if err != nil {
		t.Fatalf("Cloud Build クライアントの作成に失敗: %v", err)
	}
	defer client.Close()

	build := &cloudbuildpb.Build{
		Steps: steps,
		Options: &cloudbuildpb.BuildOptions{
			Pool: &cloudbuildpb.BuildOptions_PoolOption{
				Name: o.WorkerPoolID,
			},
		},
		ServiceAccount: fmt.Sprintf("projects/%s/serviceAccounts/%s", o.ProjectID, saEmail),
		Timeout:        durationpb.New(120 * time.Second),
	}

	op, err := client.CreateBuild(ctx, &cloudbuildpb.CreateBuildRequest{
		ProjectId: o.ProjectID,
		Build:     build,
	})
	if err != nil {
		t.Fatalf("ビルドの投入に失敗: %v", err)
	}

	// ビルド ID をログに出力
	metadata, _ := op.Metadata()
	if metadata != nil && metadata.Build != nil {
		t.Logf("ビルド ID: %s", metadata.Build.Id)
	}

	// ビルド完了まで待機（最大 5 分）
	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	resp, err := op.Wait(waitCtx)
	if err != nil {
		// タイムアウトやキャンセルの場合でもビルドステータスを返す
		t.Logf("ビルド待機中にエラー: %v", err)
		return &buildResult{Status: cloudbuildpb.Build_TIMEOUT}
	}

	return &buildResult{
		Status: resp.Status,
		Logs:   resp.LogUrl,
	}
}

// ──────────────────────────────────────────────
// Test 1: VPC 統合検証
// ──────────────────────────────────────────────

func TestPrivatePool_VPCIntegration(t *testing.T) {
	ctx := context.Background()
	o := getTerraformOutputs(t)

	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "正常系_GCSにVPC内部からアクセスできること",
			run: func(t *testing.T) {
				// テストファイルを作成し GCS にアップロード
				steps := []*cloudbuildpb.BuildStep{
					{
						Name:       "gcr.io/cloud-builders/gcloud",
						Entrypoint: "bash",
						Args: []string{
							"-c",
							fmt.Sprintf(
								`echo "hello from private pool" > /tmp/test.txt && gsutil cp /tmp/test.txt gs://%s/test.txt && echo "GCS upload succeeded"`,
								o.BucketA,
							),
						},
					},
				}
				result := submitBuild(t, ctx, o, o.SAEmailA, steps)
				if result.Status != cloudbuildpb.Build_SUCCESS {
					t.Errorf("ビルドが成功するべきところ、ステータス=%s, ログ=%s", result.Status, result.Logs)
				}
			},
		},
		{
			name: "正常系_ArtifactRegistryにVPC内部から接続できること",
			run: func(t *testing.T) {
				// AR リポジトリの一覧を取得して接続確認
				steps := []*cloudbuildpb.BuildStep{
					{
						Name:       "gcr.io/cloud-builders/gcloud",
						Entrypoint: "bash",
						Args: []string{
							"-c",
							fmt.Sprintf(
								`gcloud artifacts docker images list %s-docker.pkg.dev/%s/%s --limit=1 2>&1 && echo "AR connection succeeded"`,
								o.Region, o.ProjectID, o.ARRepoName,
							),
						},
					},
				}
				result := submitBuild(t, ctx, o, o.SAEmailA, steps)
				if result.Status != cloudbuildpb.Build_SUCCESS {
					t.Errorf("ビルドが成功するべきところ、ステータス=%s, ログ=%s", result.Status, result.Logs)
				}
			},
		},
		{
			name: "正常系_FWで許可されたIP_8.8.8.8_にアクセスできること",
			run: func(t *testing.T) {
				steps := []*cloudbuildpb.BuildStep{
					{
						Name:       "gcr.io/cloud-builders/curl",
						Entrypoint: "bash",
						Args: []string{
							"-c",
							`curl --connect-timeout 10 -s -o /dev/null -w "%{http_code}" https://dns.google && echo " - allowed IP reachable"`,
						},
					},
				}
				result := submitBuild(t, ctx, o, o.SAEmailA, steps)
				if result.Status != cloudbuildpb.Build_SUCCESS {
					t.Errorf("FW で許可された IP へのアクセスが成功するべきところ、ステータス=%s, ログ=%s", result.Status, result.Logs)
				}
			},
		},
		{
			name: "異常系_FWで許可されていないIP_1.1.1.1_にアクセスできないこと",
			run: func(t *testing.T) {
				steps := []*cloudbuildpb.BuildStep{
					{
						Name:       "gcr.io/cloud-builders/curl",
						Entrypoint: "bash",
						Args: []string{
							"-c",
							// タイムアウトを短くして失敗を確認。curl が失敗（非0終了）すればビルドも FAILURE になる
							`curl --connect-timeout 10 -s https://1.1.1.1 && echo "ERROR: should not reach here"`,
						},
					},
				}
				result := submitBuild(t, ctx, o, o.SAEmailA, steps)
				if result.Status == cloudbuildpb.Build_SUCCESS {
					t.Errorf("FW で許可されていない IP へのアクセスは失敗するべきところ、成功してしまった, ログ=%s", result.Logs)
				}
				t.Logf("期待通りビルドが失敗: ステータス=%s", result.Status)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.run(t)
		})
	}
}

// ──────────────────────────────────────────────
// Test 2: Service Account 分離検証
// ──────────────────────────────────────────────

func TestPrivatePool_ServiceAccountIsolation(t *testing.T) {
	ctx := context.Background()
	o := getTerraformOutputs(t)

	// gsutil cp でバケットアクセスを試みるビルドステップを生成するヘルパー
	gcsWriteStep := func(bucketName string) []*cloudbuildpb.BuildStep {
		return []*cloudbuildpb.BuildStep{
			{
				Name:       "gcr.io/cloud-builders/gcloud",
				Entrypoint: "bash",
				Args: []string{
					"-c",
					fmt.Sprintf(
						`echo "sa-isolation-test" > /tmp/sa-test.txt && gsutil cp /tmp/sa-test.txt gs://%s/sa-test.txt`,
						bucketName,
					),
				},
			},
		}
	}

	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "正常系_SA-AはBucket-Aにアクセスできること",
			run: func(t *testing.T) {
				result := submitBuild(t, ctx, o, o.SAEmailA, gcsWriteStep(o.BucketA))
				if result.Status != cloudbuildpb.Build_SUCCESS {
					t.Errorf("SA-A → Bucket-A は成功するべきところ、ステータス=%s, ログ=%s", result.Status, result.Logs)
				}
			},
		},
		{
			name: "異常系_SA-AはBucket-Bにアクセスできないこと",
			run: func(t *testing.T) {
				result := submitBuild(t, ctx, o, o.SAEmailA, gcsWriteStep(o.BucketB))
				if result.Status == cloudbuildpb.Build_SUCCESS {
					t.Errorf("SA-A → Bucket-B は失敗するべきところ、成功してしまった, ログ=%s", result.Logs)
				}
				t.Logf("期待通り SA-A は Bucket-B にアクセス拒否: ステータス=%s", result.Status)
			},
		},
		{
			name: "正常系_SA-BはBucket-Bにアクセスできること",
			run: func(t *testing.T) {
				result := submitBuild(t, ctx, o, o.SAEmailB, gcsWriteStep(o.BucketB))
				if result.Status != cloudbuildpb.Build_SUCCESS {
					t.Errorf("SA-B → Bucket-B は成功するべきところ、ステータス=%s, ログ=%s", result.Status, result.Logs)
				}
			},
		},
		{
			name: "異常系_SA-BはBucket-Aにアクセスできないこと",
			run: func(t *testing.T) {
				result := submitBuild(t, ctx, o, o.SAEmailB, gcsWriteStep(o.BucketA))
				if result.Status == cloudbuildpb.Build_SUCCESS {
					t.Errorf("SA-B → Bucket-A は失敗するべきところ、成功してしまった, ログ=%s", result.Logs)
				}
				t.Logf("期待通り SA-B は Bucket-A にアクセス拒否: ステータス=%s", result.Status)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.run(t)
		})
	}
}

// ──────────────────────────────────────────────
// ヘルパー: ビルドログから出力を取得（デバッグ用）
// ──────────────────────────────────────────────

func fetchBuildLogs(t *testing.T, projectID, buildID string) string {
	t.Helper()
	cmd := exec.Command("gcloud", "builds", "log", buildID, "--project", projectID)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("ビルドログの取得に失敗: %v", err)
		return ""
	}
	return strings.TrimSpace(string(out))
}
