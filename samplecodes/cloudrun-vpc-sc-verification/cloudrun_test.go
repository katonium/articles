package cloudrun_vpc_sc_verification_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"testing"
	"time"

	run "cloud.google.com/go/run/apiv2"
	runpb "cloud.google.com/go/run/apiv2/runpb"
	"google.golang.org/api/option"
)

type tfOutput struct {
	BaseProjectID    string
	GuestAProjectID  string
	GuestBProjectID  string
	Region           string
	Suffix           string
	Case0JobName     string
	Case2JobName     string
	Case3JobName     string
	Case4JobName     string
	Case5JobName     string
	BaseBucket       string
	BaseDataset      string
	GuestABucket     string
	GuestADataset    string
	GuestBBucket     string
	GuestBDataset    string
	PerimeterAName   string
	PerimeterBName   string
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
		Value any `json:"value"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		t.Fatalf("terraform output の解析に失敗: %v", err)
	}

	getString := func(key string) string {
		v, ok := raw[key]
		if !ok {
			t.Fatalf("terraform output に %s が見つかりません", key)
		}
		switch x := v.Value.(type) {
		case string:
			return x
		case float64:
			return fmt.Sprintf("%.0f", x)
		default:
			return fmt.Sprintf("%v", x)
		}
	}

	return &tfOutput{
		BaseProjectID:   getString("base_project_id"),
		GuestAProjectID: getString("guest_a_project_id"),
		GuestBProjectID: getString("guest_b_project_id"),
		Region:          getString("region"),
		Suffix:          getString("suffix"),
		Case0JobName:    getString("case0_base_job_name"),
		Case2JobName:    getString("case2_ga_host_job_name"),
		Case3JobName:    getString("case3_ga_self_job_name"),
		Case4JobName:    getString("case4_gb_host_job_name"),
		Case5JobName:    getString("case5_gb_self_job_name"),
		BaseBucket:      getString("base_bucket"),
		BaseDataset:     getString("base_dataset"),
		GuestABucket:    getString("guest_a_bucket"),
		GuestADataset:   getString("guest_a_dataset"),
		GuestBBucket:    getString("guest_b_bucket"),
		GuestBDataset:   getString("guest_b_dataset"),
		PerimeterAName:  getString("perimeter_a_name"),
		PerimeterBName:  getString("perimeter_b_name"),
	}
}

// executionResult は Cloud Run Job 実行結果のまとめ。
//
//   - Status は op.Wait の結果 (成功時のみ Execution が返り、エラー時は err)
//   - Exec は最終の Execution proto (Wait 失敗時は op.Metadata() の戻り値、それも無ければ nil)
//   - WaitErr は op.Wait が返したエラー (deny 系の検証で値を見たい)
type executionResult struct {
	Exec    *runpb.Execution
	WaitErr error
}

// IsSucceeded は SucceededCount >= 1 でジョブが成功したと判定する。
// (task_count = 1 で動かしているので SucceededCount == 1 で SUCCESS、 FailedCount >= 1 で FAILURE)
func (r *executionResult) IsSucceeded() bool {
	if r.WaitErr != nil {
		return false
	}
	if r.Exec == nil {
		return false
	}
	return r.Exec.GetSucceededCount() >= 1 && r.Exec.GetFailedCount() == 0
}

// executeJob は Cloud Run Job を 1 回 実行し、 Execution が完了するまで待つ。
// project は Job が属するプロジェクト、 region は Job のロケーション、
// jobName は google_cloud_run_v2_job.name 出力値 (フル resource path ではない単純名)。
// Cloud Run V2 API はリージョナルエンドポイントを使う必要があるため、
// option.WithEndpoint で <region>-run.googleapis.com:443 を指定する。
func executeJob(t *testing.T, ctx context.Context, project, region, jobName string, timeout time.Duration) *executionResult {
	t.Helper()

	endpoint := fmt.Sprintf("%s-run.googleapis.com:443", region)
	client, err := run.NewJobsClient(ctx, option.WithEndpoint(endpoint))
	if err != nil {
		t.Fatalf("Cloud Run Jobs クライアントの作成に失敗: %v", err)
	}
	defer client.Close()

	fullName := fmt.Sprintf("projects/%s/locations/%s/jobs/%s", project, region, jobName)

	op, err := client.RunJob(ctx, &runpb.RunJobRequest{
		Name: fullName,
	})
	if err != nil {
		t.Fatalf("RunJob の呼び出しに失敗 name=%s: %v", fullName, err)
	}

	if meta, mErr := op.Metadata(); mErr == nil && meta != nil {
		t.Logf("Execution 開始 name=%s", meta.GetName())
	}

	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	resp, err := op.Wait(waitCtx)
	if err != nil {
		t.Logf("op.Wait エラー (deny ケースでは想定): %v", err)
		if meta, mErr := op.Metadata(); mErr == nil && meta != nil {
			return &executionResult{Exec: meta, WaitErr: err}
		}
		return &executionResult{WaitErr: err}
	}

	t.Logf("Execution 完了 name=%s succeeded=%d failed=%d cancelled=%d",
		resp.GetName(), resp.GetSucceededCount(), resp.GetFailedCount(), resp.GetCancelledCount())
	return &executionResult{Exec: resp}
}

// ──────────────────────────────────────────────
// Case 0: base / base connector / base resource (同一 project, Perim A 内)
//
// 期待:
//   Cloud Run Job は SUCCESS。 connector 経由で *.googleapis.com → restricted VIP
//   に DNS 解決され、 base project 内の GCS / BQ にアクセスできる。
// ──────────────────────────────────────────────

func TestCase0_BaseInPerimSameProject_Succeeds(t *testing.T) {
	ctx := context.Background()
	o := getTerraformOutputs(t)

	res := executeJob(t, ctx, o.BaseProjectID, o.Region, o.Case0JobName, 10*time.Minute)
	if !res.IsSucceeded() {
		t.Fatalf("Case 0 (同 perim 同 project) は SUCCESS のはず: succeeded=%d failed=%d waitErr=%v",
			res.Exec.GetSucceededCount(), res.Exec.GetFailedCount(), res.WaitErr)
	}
}

// ──────────────────────────────────────────────
// Case 2: guest_a / guest_a connector / base resource (Shared VPC, Perim A 内 cross-project)
//
// 期待:
//   guest_a の Cloud Run Job が host (base) の GCS / BQ にアクセスする。
//   両者とも Perim A 配下なので、Ingress policy 経由ではなく「same-perim」で抜ける。
//   IAM は 06_iam.tf で job_guest_a に base の reader を付けている。
//   Job は SUCCESS。
// ──────────────────────────────────────────────

func TestCase2_GuestASharedVPC_HostResource_Succeeds(t *testing.T) {
	ctx := context.Background()
	o := getTerraformOutputs(t)

	res := executeJob(t, ctx, o.GuestAProjectID, o.Region, o.Case2JobName, 10*time.Minute)
	if !res.IsSucceeded() {
		t.Fatalf("Case 2 (guest_a → base, 同 perim) は SUCCESS のはず: succeeded=%d failed=%d waitErr=%v",
			res.Exec.GetSucceededCount(), res.Exec.GetFailedCount(), res.WaitErr)
	}
}

// ──────────────────────────────────────────────
// Case 3: guest_a / guest_a connector / guest_a resource (同一 project, Perim A 内)
//
// 期待:
//   guest_a の Cloud Run Job が同じ guest_a project の GCS / BQ にアクセス。
//   IAM 付与済み、 同 perim、 同 project なので SUCCESS。
// ──────────────────────────────────────────────

func TestCase3_GuestASharedVPC_SelfResource_Succeeds(t *testing.T) {
	ctx := context.Background()
	o := getTerraformOutputs(t)

	res := executeJob(t, ctx, o.GuestAProjectID, o.Region, o.Case3JobName, 10*time.Minute)
	if !res.IsSucceeded() {
		t.Fatalf("Case 3 (guest_a 自身) は SUCCESS のはず: succeeded=%d failed=%d waitErr=%v",
			res.Exec.GetSucceededCount(), res.Exec.GetFailedCount(), res.WaitErr)
	}
}

// ──────────────────────────────────────────────
// Case 4: guest_b / guest_b connector / base resource (Perim B → Perim A, deny 期待)
//
// 期待:
//   guest_b は Perimeter B、 base resource は Perimeter A。 cross-perimeter
//   アクセスは VPC-SC で deny されるはず。 さらに guest_b の Cloud Run Job 自体
//   が cross-perim Shared VPC (host=base は Perim A) の connector を持つため、
//   そもそも connector network が立ち上がらない可能性もある。 どちらにせよ
//   Job は SUCCESS しない (FAILED / 起動できず / op.Wait timeout のいずれか)。
// ──────────────────────────────────────────────

func TestCase4_GuestBSplitPerimeter_HostResource_Denied(t *testing.T) {
	ctx := context.Background()
	o := getTerraformOutputs(t)

	// cross-perim では起動できない or timeout もありうるので、短めの timeout で切り上げ。
	res := executeJob(t, ctx, o.GuestBProjectID, o.Region, o.Case4JobName, 8*time.Minute)
	if res.IsSucceeded() {
		t.Fatalf("Case 4 (guest_b → base, cross-perim) は deny されるはずだが SUCCESS した: succeeded=%d failed=%d",
			res.Exec.GetSucceededCount(), res.Exec.GetFailedCount())
	}
	t.Logf("Case 4: 期待通り SUCCESS しなかった waitErr=%v", res.WaitErr)
}

// ──────────────────────────────────────────────
// Case 5: guest_b / guest_b connector / guest_b resource (Perim B 内, in-project)
//
// 当初の素朴な仮説:
//   guest_b と guest_b の resource は同じ Perimeter B 配下、 IAM も付与済みなので
//   同 perim 内アクセスとして SUCCESS する。
//
// 実際に観測されうる挙動 (cloudbuild-private-pool の Case 4 と同じ理由):
//   guest_b の connector は Shared VPC host (= base = Perim A) の subnet を使う。
//   この cross-perim Shared VPC により connector の network が成立しない可能性が高い。
//   すると connector を介した egress 全般が動かなくなり、 same-perim の自リソース
//   にも到達できない (worker 起動不能 or connection refused / timeout)。
//
// 期待:
//   Job は SUCCESS しない。 deny 原因は「同 perim deny」ではなく「Shared VPC
//   cross-perim で connector が機能しない」という Cloud Run + Shared VPC + VPC-SC
//   の合成挙動による。
// ──────────────────────────────────────────────

func TestCase5_GuestBSplitPerimeter_SelfResource_BlockedByCrossPerimSharedVPC(t *testing.T) {
	ctx := context.Background()
	o := getTerraformOutputs(t)

	res := executeJob(t, ctx, o.GuestBProjectID, o.Region, o.Case5JobName, 8*time.Minute)
	if res.IsSucceeded() {
		t.Fatalf("Case 5 (guest_b 自リソース, cross-perim Shared VPC) は SUCCESS しないはずだが succeeded=%d failed=%d",
			res.Exec.GetSucceededCount(), res.Exec.GetFailedCount())
	}
	t.Logf("Case 5: 期待通り SUCCESS しなかった waitErr=%v", res.WaitErr)
}
