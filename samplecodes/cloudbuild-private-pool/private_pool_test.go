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
		ProjectID:       getString("project_id"),
		ProjectNumber:   getString("project_number"),
		Region:          getString("region"),
		Suffix:          getString("suffix"),
		WorkerPoolID:    getString("worker_pool_id"),
		BuildSAEmail:    getString("build_sa_email"),
		DockerHubMirror: getString("dockerhub_mirror_repo"),
		PyPIMirror:      getString("pypi_mirror_repo"),
		BuildOutputRepo: getString("build_output_repo"),
		PerimeterName:   getString("perimeter_name"),
	}
}

type buildResult struct {
	Status  cloudbuildpb.Build_Status
	LogURL  string
	BuildID string
}

// submitBuild は Private Pool に Build を投入し、完了まで待つ。
func submitBuild(t *testing.T, ctx context.Context, o *tfOutput, steps []*cloudbuildpb.BuildStep) *buildResult {
	t.Helper()

	// Private Pool はリージョナル API を使う必要がある
	endpoint := fmt.Sprintf("%s-cloudbuild.googleapis.com:443", o.Region)
	client, err := cloudbuild.NewClient(ctx, option.WithEndpoint(endpoint))
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
			// Private Pool + Custom SA では CLOUD_LOGGING_ONLY が必要
			Logging: cloudbuildpb.BuildOptions_CLOUD_LOGGING_ONLY,
			// bash 側の ${VAR} を Cloud Build の substitution として解釈させない
			SubstitutionOption: cloudbuildpb.BuildOptions_ALLOW_LOOSE,
		},
		ServiceAccount: fmt.Sprintf("projects/%s/serviceAccounts/%s", o.ProjectID, o.BuildSAEmail),
		Timeout:        durationpb.New(15 * time.Minute),
	}

	op, err := client.CreateBuild(ctx, &cloudbuildpb.CreateBuildRequest{
		ProjectId: o.ProjectID,
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

	waitCtx, cancel := context.WithTimeout(ctx, 20*time.Minute)
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

	dockerHost := fmt.Sprintf("%s-docker.pkg.dev", o.Region)
	pypiHost := fmt.Sprintf("%s-python.pkg.dev", o.Region)
	baseImage := fmt.Sprintf("%s/%s/%s/library/python:3.12-slim", dockerHost, o.ProjectID, o.DockerHubMirror)
	pushImage := fmt.Sprintf("%s/%s/%s/vpcsc-test:%s", dockerHost, o.ProjectID, o.BuildOutputRepo, o.Suffix)

	// 単一 step。step.name に AR mirror image を指定すると worker daemon の認証導線
	// (P4SA / hidden agent) が絡んで pull denied になりやすいため、step.name は
	// google-managed の gcr.io/cloud-builders/docker で固定し、AR mirror へのアクセスは
	// すべて step 内の docker daemon (= build SA 認証) で行う。
	//
	// $$ は Cloud Build の substitution エスケープ。Cloud Build がパース後に $ 1 個になり、
	// bash や Dockerfile の変数展開として機能する。
	// 王道: Cloud Build worker docker daemon は同 project の Artifact Registry に
	// auto-auth (build SA の identity)。docker login / gcloud auth configure-docker
	// は不要で、不適切に呼ぶと書き込み先と daemon の credential 経路がずれて壊れる。
	// PyPI mirror への pip install だけは Dockerfile の RUN 内なので、別途 metadata
	// server から build SA の token を取って --build-arg 経由で渡す。
	script := fmt.Sprintf(`set -euo pipefail

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
		pypiHost, o.ProjectID, o.PyPIMirror,
		pushImage, pushImage,
	)

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
