package cloudbuild_private_pool_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"testing"
	"time"

	cloudbuild "cloud.google.com/go/cloudbuild/apiv1/v2"
	cloudbuildpb "cloud.google.com/go/cloudbuild/apiv1/v2/cloudbuildpb"
	"google.golang.org/api/option"
	"google.golang.org/protobuf/types/known/durationpb"
)

type tfOutput struct {
	ProjectID       string
	ProjectNumber   string
	Region          string
	Suffix          string
	WorkerPoolID    string
	BuildSAEmail    string
	DockerHubMirror string
	PyPIMirror      string
	BuildOutputRepo string
	PerimeterName   string

	// Case 1 / Case 2b
	NopeerPoolID   string
	DefrangePoolID string

	// Case 3 (guest_a / Shared VPC)
	GuestAProjectID       string
	GuestAPoolID          string
	GuestABuildSAEmail    string
	GuestADockerhubMirror string
	GuestAPyPIMirror      string
	GuestABuildOutputRepo string

	// Case 4 (guest_b / Split Perimeter)
	GuestBProjectID       string
	GuestBPoolID          string
	GuestBBuildSAEmail    string
	GuestBDockerhubMirror string
	GuestBPyPIMirror      string
	GuestBBuildOutputRepo string
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
		ProjectID:       getString("base_project_id"),
		ProjectNumber:   getString("base_project_number"),
		Region:          getString("region"),
		Suffix:          getString("suffix"),
		WorkerPoolID:    getString("worker_pool_id"),
		BuildSAEmail:    getString("build_sa_email"),
		DockerHubMirror: getString("dockerhub_mirror_repo"),
		PyPIMirror:      getString("pypi_mirror_repo"),
		BuildOutputRepo: getString("build_output_repo"),
		PerimeterName:   getString("perimeter_a_name"),

		NopeerPoolID:   getString("nopeer_pool_id"),
		DefrangePoolID: getString("defrange_pool_id"),

		GuestAProjectID:       getString("guest_a_project_id"),
		GuestAPoolID:          getString("guest_a_pool_id"),
		GuestABuildSAEmail:    getString("guest_a_build_sa_email"),
		GuestADockerhubMirror: getString("guest_a_dockerhub_mirror_repo"),
		GuestAPyPIMirror:      getString("guest_a_pypi_mirror_repo"),
		GuestABuildOutputRepo: getString("guest_a_build_output_repo"),

		GuestBProjectID:       getString("guest_b_project_id"),
		GuestBPoolID:          getString("guest_b_pool_id"),
		GuestBBuildSAEmail:    getString("guest_b_build_sa_email"),
		GuestBDockerhubMirror: getString("guest_b_dockerhub_mirror_repo"),
		GuestBPyPIMirror:      getString("guest_b_pypi_mirror_repo"),
		GuestBBuildOutputRepo: getString("guest_b_build_output_repo"),
	}
}

type buildResult struct {
	Status  cloudbuildpb.Build_Status
	LogURL  string
	BuildID string
}

// buildSubmitOpts は submitBuildWith に渡すオプション。
//
// Case 3 のように pool / build SA が base 以外のプロジェクトに属する場合、
// builds.create の projectId と Pool.Name はそのプロジェクトを指す必要がある。
type buildSubmitOpts struct {
	ProjectID      string // builds.create の対象 project (= pool が属する project)
	Region         string // リージョナル API endpoint 用
	WorkerPoolID   string // projects/.../locations/.../workerPools/... のフルリソース名
	BuildSAEmail   string // build SA email
	BuildSAProject string // SA が属する project (resource path 構築用)
	Timeout        time.Duration
}

func defaultSubmitOpts(o *tfOutput) *buildSubmitOpts {
	return &buildSubmitOpts{
		ProjectID:      o.ProjectID,
		Region:         o.Region,
		WorkerPoolID:   o.WorkerPoolID,
		BuildSAEmail:   o.BuildSAEmail,
		BuildSAProject: o.ProjectID,
		Timeout:        15 * time.Minute,
	}
}

// submitBuild は base project / 既定の pool & SA で Build を投入する (Case 0 互換)。
func submitBuild(t *testing.T, ctx context.Context, o *tfOutput, steps []*cloudbuildpb.BuildStep) *buildResult {
	t.Helper()
	return submitBuildWith(t, ctx, defaultSubmitOpts(o), steps)
}

// submitBuildWith は任意の project / pool / SA に対して Build を投入する。
func submitBuildWith(t *testing.T, ctx context.Context, opts *buildSubmitOpts, steps []*cloudbuildpb.BuildStep) *buildResult {
	t.Helper()

	if opts.Timeout == 0 {
		opts.Timeout = 15 * time.Minute
	}

	// Private Pool はリージョナル API を使う必要がある
	endpoint := fmt.Sprintf("%s-cloudbuild.googleapis.com:443", opts.Region)
	client, err := cloudbuild.NewClient(ctx, option.WithEndpoint(endpoint))
	if err != nil {
		t.Fatalf("Cloud Build クライアントの作成に失敗: %v", err)
	}
	defer client.Close()

	build := &cloudbuildpb.Build{
		Steps: steps,
		Options: &cloudbuildpb.BuildOptions{
			Pool: &cloudbuildpb.BuildOptions_PoolOption{
				Name: opts.WorkerPoolID,
			},
			// Private Pool + Custom SA では CLOUD_LOGGING_ONLY が必要
			Logging: cloudbuildpb.BuildOptions_CLOUD_LOGGING_ONLY,
			// bash 側の ${VAR} を Cloud Build の substitution として解釈させない
			SubstitutionOption: cloudbuildpb.BuildOptions_ALLOW_LOOSE,
		},
		ServiceAccount: fmt.Sprintf("projects/%s/serviceAccounts/%s", opts.BuildSAProject, opts.BuildSAEmail),
		Timeout:        durationpb.New(opts.Timeout),
	}

	op, err := client.CreateBuild(ctx, &cloudbuildpb.CreateBuildRequest{
		ProjectId: opts.ProjectID,
		Build:     build,
	})
	if err != nil {
		t.Fatalf("ビルドの投入に失敗: %v", err)
	}

	buildID := ""
	if meta, mErr := op.Metadata(); mErr == nil && meta != nil && meta.Build != nil {
		buildID = meta.Build.Id
		t.Logf("ビルド開始 id=%s log=%s", meta.Build.Id, meta.Build.LogUrl)
	}

	// op.Wait の待ち時間は build timeout より少し長めに取る
	waitTimeout := opts.Timeout + 5*time.Minute
	waitCtx, cancel := context.WithTimeout(ctx, waitTimeout)
	defer cancel()

	resp, err := op.Wait(waitCtx)
	if err != nil {
		t.Logf("op.Wait エラー: %v", err)
		if meta, mErr := op.Metadata(); mErr == nil && meta != nil && meta.Build != nil {
			return &buildResult{Status: meta.Build.Status, LogURL: meta.Build.LogUrl, BuildID: meta.Build.Id}
		}
		return &buildResult{Status: cloudbuildpb.Build_FAILURE, BuildID: buildID}
	}

	return &buildResult{Status: resp.Status, LogURL: resp.LogUrl, BuildID: resp.Id}
}

// dockerBuildScript は AR mirror から base image を pull → pip mirror から
// requests を入れて build → AR build-output に push する shell script を作る。
//
// project / region / repo を引数化しているので、 base / guest_a どちらでも使える。
func dockerBuildScript(region, project, dockerhubRepo, pypiRepo, buildOutputRepo, suffix string) (script, pushImage string) {
	dockerHost := fmt.Sprintf("%s-docker.pkg.dev", region)
	pypiHost := fmt.Sprintf("%s-python.pkg.dev", region)
	baseImage := fmt.Sprintf("%s/%s/%s/library/python:3.12-slim", dockerHost, project, dockerhubRepo)
	pushImage = fmt.Sprintf("%s/%s/%s/vpcsc-test:%s", dockerHost, project, buildOutputRepo, suffix)

	script = fmt.Sprintf(`set -euo pipefail

TOKEN=$$(curl -fsS -H "Metadata-Flavor: Google" \
  http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/token \
  | sed -n 's/.*"access_token":"\([^"]*\)".*/\1/p')
test -n "$${TOKEN}" || { echo "metadata token empty"; exit 1; }

cat > Dockerfile <<'EOF'
FROM %s
ARG PIP_INDEX
RUN pip install --no-cache-dir --index-url "$${PIP_INDEX}" requests \
 && python -c "import requests; print('requests version:', requests.__version__)"
CMD ["python", "-c", "print('image built inside VPC-SC')"]
EOF

docker build \
  --build-arg "PIP_INDEX=https://oauth2accesstoken:$${TOKEN}@%s/%s/%s/simple/" \
  -t %s .
docker push %s
`,
		baseImage,
		pypiHost, project, pypiRepo,
		pushImage, pushImage,
	)
	return script, pushImage
}

// ──────────────────────────────────────────────
// Test: Private Pool 内 Docker build が VPC-SC 境界内 AR にアクセスできること
//
// 検証内容:
//  1. AR DockerHub mirror から python:3.12-slim を base image として pull
//  2. AR PyPI mirror から requests を pip install
//  3. Dockerfile を build し、AR build-output repo に push
//
// 期待:
//   Ingress policy には var.ingress_identities の identity のみが入っており、
//   Build job 用 Service Account は含まれていない。それでも 境界内 (VPC) →
//   境界内 (AR) は Ingress 不要で通るため、すべてのステップが成功する。
// ──────────────────────────────────────────────

func TestVPCSCPrivatePool_DockerBuild(t *testing.T) {
	ctx := context.Background()
	o := getTerraformOutputs(t)

	script, _ := dockerBuildScript(o.Region, o.ProjectID, o.DockerHubMirror, o.PyPIMirror, o.BuildOutputRepo, o.Suffix)

	steps := []*cloudbuildpb.BuildStep{
		{
			Id:         "build-via-mirrors-and-push",
			Name:       "gcr.io/cloud-builders/docker",
			Entrypoint: "bash",
			Args:       []string{"-c", script},
		},
	}

	res := submitBuild(t, ctx, o, steps)
	if res.Status != cloudbuildpb.Build_SUCCESS {
		t.Fatalf("ビルドが成功するべきところ status=%s log=%s build_id=%s",
			res.Status, res.LogURL, res.BuildID)
	}
	t.Logf("ビルド成功 build_id=%s log=%s", res.BuildID, res.LogURL)
}

// ──────────────────────────────────────────────
// Test: 外部直 (DockerHub 公式 / PyPI 公式) アクセスは拒否されること
//
// 検証内容:
//   AR mirror を経由せず、docker.io / pypi.org に直接アクセスするビルドを投入。
// 期待:
//   FW で deny-all-egress + restricted.googleapis のみ許可なので、
//   外部 IP に出られず timeout / network unreachable で失敗する。
// ──────────────────────────────────────────────

func TestVPCSCPrivatePool_ExternalAccessDenied(t *testing.T) {
	ctx := context.Background()
	o := getTerraformOutputs(t)

	// step.name は google-managed の builder image にし、テスト本体 (curl) を step 内で実行する。
	// これにより、step.name pull の失敗で誤って「外部遮断成功」と判定される事故を避ける。
	script := `set -euo pipefail
# 公式 PyPI への直アクセス。FW で 0.0.0.0/0 への egress は restricted VIP 以外
# deny されているため、connect timeout して非ゼロ終了するはず。
curl --connect-timeout 10 -fsS https://pypi.org/simple/ -o /tmp/idx.html
echo "ERROR: should not reach here"
exit 1
`

	steps := []*cloudbuildpb.BuildStep{
		{
			Id:         "external-access-should-fail",
			Name:       "gcr.io/cloud-builders/docker",
			Entrypoint: "bash",
			Args:       []string{"-c", script},
		},
	}

	res := submitBuild(t, ctx, o, steps)
	if res.Status == cloudbuildpb.Build_SUCCESS {
		t.Fatalf("外部直アクセスは拒否されるべきだが成功した log=%s build_id=%s",
			res.LogURL, res.BuildID)
	}
	t.Logf("期待通り外部直アクセスは失敗 status=%s build_id=%s", res.Status, res.BuildID)
}

// ──────────────────────────────────────────────
// Case 1: peering なし pool では AR にアクセスできない (negative)
//
// 検証内容:
//   nopeer_pool は Service Networking peering なし、 *.googleapis.com /
//   *.pkg.dev の DNS routing も設定されていない。AR は到達不能になるはず。
// 期待:
//   docker pull が DNS / 接続失敗で非ゼロ終了し、ビルドは FAILURE / TIMEOUT。
// ──────────────────────────────────────────────

func TestCase1_NoPeering_DockerBuildFails(t *testing.T) {
	ctx := context.Background()
	o := getTerraformOutputs(t)

	dockerHost := fmt.Sprintf("%s-docker.pkg.dev", o.Region)
	baseImage := fmt.Sprintf("%s/%s/%s/library/python:3.12-slim", dockerHost, o.ProjectID, o.DockerHubMirror)

	// docker pull は内部で長く粘ることがあるので、curl で *.pkg.dev に到達できないこと
	// を先に確認 → 続いて docker pull を撃って fail を待つ。どちらにせよ非ゼロで終わる。
	script := fmt.Sprintf(`set -euo pipefail
# DNS / 到達確認。 peering なしなので *.pkg.dev は名前解決できないか接続できない。
echo "trying to reach %s ..."
curl --connect-timeout 30 -fsS -o /dev/null https://%s/v2/ || echo "curl failed as expected"

echo "now trying docker pull, this should also fail ..."
docker pull %s
echo "ERROR: docker pull unexpectedly succeeded"
exit 1
`, dockerHost, dockerHost, baseImage)

	steps := []*cloudbuildpb.BuildStep{
		{
			Id:         "nopeer-should-fail",
			Name:       "gcr.io/cloud-builders/docker",
			Entrypoint: "bash",
			Args:       []string{"-c", script},
		},
	}

	opts := defaultSubmitOpts(o)
	opts.WorkerPoolID = o.NopeerPoolID
	// peering なしの pool は接続が確立しない → step が長く粘る前に短めの timeout で切り上げる
	opts.Timeout = 5 * time.Minute

	res := submitBuildWith(t, ctx, opts, steps)
	if res.Status == cloudbuildpb.Build_SUCCESS {
		t.Fatalf("peering なし pool で AR pull が成功してしまった (失敗するべき) log=%s build_id=%s",
			res.LogURL, res.BuildID)
	}
	t.Logf("期待通り peering なし pool では AR 到達不可 status=%s build_id=%s", res.Status, res.BuildID)
}

// ──────────────────────────────────────────────
// Case 2b: peered_network_ip_range 省略 (default) でも build は成功する (positive)
//
// 検証内容:
//   defrange_pool は Service Networking peering ありで peered_network_ip_range を
//   未指定 (Google が自動で /24 を取る)。これでも Case 0 と同等の docker build が
//   通るはず。
// 期待:
//   ビルド SUCCESS。
// ──────────────────────────────────────────────

func TestCase2b_DefaultPeeredRange_DockerBuildSucceeds(t *testing.T) {
	ctx := context.Background()
	o := getTerraformOutputs(t)

	script, _ := dockerBuildScript(o.Region, o.ProjectID, o.DockerHubMirror, o.PyPIMirror, o.BuildOutputRepo, o.Suffix+"-defrange")

	steps := []*cloudbuildpb.BuildStep{
		{
			Id:         "defrange-build-via-mirrors-and-push",
			Name:       "gcr.io/cloud-builders/docker",
			Entrypoint: "bash",
			Args:       []string{"-c", script},
		},
	}

	opts := defaultSubmitOpts(o)
	opts.WorkerPoolID = o.DefrangePoolID

	res := submitBuildWith(t, ctx, opts, steps)
	if res.Status != cloudbuildpb.Build_SUCCESS {
		t.Fatalf("defrange pool でビルドが成功するべきところ status=%s log=%s build_id=%s",
			res.Status, res.LogURL, res.BuildID)
	}
	t.Logf("defrange pool でビルド成功 build_id=%s log=%s", res.BuildID, res.LogURL)
}

// ──────────────────────────────────────────────
// Case 3: Shared VPC + guest_a project の pool で in-project AR を使う (positive)
//
// 検証内容:
//   guest_a_pool は Shared VPC (host = base) を使う pool だが、 pool 自体と
//   build SA / AR mirror / build-output は guest_a project に属する。
//   builds.create の projectId は guest_a を指定する必要がある。
// 期待:
//   guest_a の AR mirror から pull → guest_a の AR build-output に push が成功し、
//   ビルド SUCCESS。
// ──────────────────────────────────────────────

func TestCase3_SharedVPC_GuestPool_PullGuestAR(t *testing.T) {
	ctx := context.Background()
	o := getTerraformOutputs(t)

	script, _ := dockerBuildScript(
		o.Region,
		o.GuestAProjectID,
		o.GuestADockerhubMirror,
		o.GuestAPyPIMirror,
		o.GuestABuildOutputRepo,
		o.Suffix+"-guesta",
	)

	steps := []*cloudbuildpb.BuildStep{
		{
			Id:         "guesta-build-via-mirrors-and-push",
			Name:       "gcr.io/cloud-builders/docker",
			Entrypoint: "bash",
			Args:       []string{"-c", script},
		},
	}

	opts := &buildSubmitOpts{
		ProjectID:      o.GuestAProjectID,
		Region:         o.Region,
		WorkerPoolID:   o.GuestAPoolID,
		BuildSAEmail:   o.GuestABuildSAEmail,
		BuildSAProject: o.GuestAProjectID,
		Timeout:        15 * time.Minute,
	}

	res := submitBuildWith(t, ctx, opts, steps)
	if res.Status != cloudbuildpb.Build_SUCCESS {
		t.Fatalf("guest_a pool でビルドが成功するべきところ status=%s log=%s build_id=%s",
			res.Status, res.LogURL, res.BuildID)
	}
	t.Logf("guest_a pool でビルド成功 build_id=%s log=%s", res.BuildID, res.LogURL)
}

// ──────────────────────────────────────────────
// Case 4: 境界分離 (Split Perimeter) — guest_b pool で guest_b 自身の AR を pull (実測: 失敗)
//
// 当初の仮説:
//   guest_b は Perimeter B (separate perimeter)。 guest_b_pool / guest_b_build_sa /
//   guest_b の AR mirror & build-output はすべて Perimeter B 配下にあり、
//   同一境界内アクセスなので Ingress 不要で通る → SUCCESS するはず。
//
// 観測された実際の挙動:
//   cross-perim Shared VPC では pool worker が起動できず QUEUED stuck になる。
//   build submit 自体は受理されるが、 worker が永久に scheduled されず、
//   20 分以上待っても build は QUEUED のまま動かない。 その結果として、
//   同 perim B 内の AR アクセスすら成立しない (worker そのものが動かないため)。
//   つまり「個別 deny」ではなく「worker pool が機能しない」という上位の壁が
//   cross-perimeter Shared VPC で発生する。
//
// 現在の期待:
//   ビルドは SUCCESS しない (QUEUED stuck → op.Wait timeout、 または FAILURE)。
// ──────────────────────────────────────────────

func TestCase4_SplitPerimeter_GuestPool_PullGuestAR(t *testing.T) {
	ctx := context.Background()
	o := getTerraformOutputs(t)

	script, _ := dockerBuildScript(
		o.Region,
		o.GuestBProjectID,
		o.GuestBDockerhubMirror,
		o.GuestBPyPIMirror,
		o.GuestBBuildOutputRepo,
		o.Suffix+"-guestb",
	)

	steps := []*cloudbuildpb.BuildStep{
		{
			Id:         "guestb-build-via-mirrors-and-push",
			Name:       "gcr.io/cloud-builders/docker",
			Entrypoint: "bash",
			Args:       []string{"-c", script},
		},
	}

	opts := &buildSubmitOpts{
		ProjectID:      o.GuestBProjectID,
		Region:         o.Region,
		WorkerPoolID:   o.GuestBPoolID,
		BuildSAEmail:   o.GuestBBuildSAEmail,
		BuildSAProject: o.GuestBProjectID,
		// cross-perim Shared VPC では worker が起動しないことが既知。
		// 15 min も待つ必要が無いので、 build timeout を短くして高速に終わらせる
		// (op.Wait は +5min wrap するので合計 ~10 min cap)。
		Timeout: 5 * time.Minute,
	}

	res := submitBuildWith(t, ctx, opts, steps)
	if res.Status == cloudbuildpb.Build_SUCCESS {
		t.Fatalf("cross-perim Shared VPC で worker が動作するはずがないが SUCCESS した status=%s log=%s build_id=%s",
			res.Status, res.LogURL, res.BuildID)
	}
	t.Logf("期待通り cross-perim Shared VPC で worker 起動せず status=%s build_id=%s log=%s",
		res.Status, res.BuildID, res.LogURL)
}

// ──────────────────────────────────────────────
// Case 4: 境界分離 (Split Perimeter) — guest_b pool から host (base) AR を pull (negative)
//
// 検証内容:
//   guest_b は Perimeter B、 base AR は Perimeter A。 cross-perimeter アクセスは
//   VPC-SC で deny されるはず (Ingress policy も guest_b の build SA は持っていない)。
// 期待:
//   ビルド NOT SUCCESS。 deny 原因は VPC-SC または IAM のどちらでも OK。
// ──────────────────────────────────────────────

func TestCase4_SplitPerimeter_GuestPool_PullHostAR_Denied(t *testing.T) {
	ctx := context.Background()
	o := getTerraformOutputs(t)

	// build script は base project の AR mirror / build-output を指す。
	// pool / build SA は guest_b 側 → cross-perimeter (Perimeter B → Perimeter A) になる。
	script, _ := dockerBuildScript(
		o.Region,
		o.ProjectID,
		o.DockerHubMirror,
		o.PyPIMirror,
		o.BuildOutputRepo,
		o.Suffix+"-guestb-x",
	)

	steps := []*cloudbuildpb.BuildStep{
		{
			Id:         "guestb-pull-host-ar-should-fail",
			Name:       "gcr.io/cloud-builders/docker",
			Entrypoint: "bash",
			Args:       []string{"-c", script},
		},
	}

	opts := &buildSubmitOpts{
		ProjectID:      o.GuestBProjectID,
		Region:         o.Region,
		WorkerPoolID:   o.GuestBPoolID,
		BuildSAEmail:   o.GuestBBuildSAEmail,
		BuildSAProject: o.GuestBProjectID,
		// cross-perimeter は失敗確定なので短めに切り上げ
		Timeout: 5 * time.Minute,
	}

	res := submitBuildWith(t, ctx, opts, steps)
	if res.Status == cloudbuildpb.Build_SUCCESS {
		t.Fatalf("cross-perimeter pull が成功してしまった (失敗するべき) log=%s build_id=%s",
			res.LogURL, res.BuildID)
	}
	t.Logf("期待通り cross-perimeter pull は失敗 status=%s build_id=%s", res.Status, res.BuildID)
}
