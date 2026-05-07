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

	pipIndexTmpl := fmt.Sprintf("https://oauth2accesstoken:${TOKEN}@%s/%s/%s/simple/",
		pypiHost, o.ProjectID, o.PyPIMirror)

	step1Script := fmt.Sprintf(`set -euo pipefail
TOKEN=$(curl -fsS -H "Metadata-Flavor: Google" http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/token | python -c "import sys,json;print(json.load(sys.stdin)['access_token'])")
pip install --no-cache-dir --index-url "%s" requests
python -c "import requests; print('requests version:', requests.__version__)"
`, pipIndexTmpl)

	step2Script := fmt.Sprintf(`set -euo pipefail
cat > Dockerfile <<'EOF'
FROM %s
RUN python -c "print('hello from VPC-SC build')"
CMD ["python", "-c", "print('image built inside VPC-SC')"]
EOF
docker build -t %s .
docker push %s
`, baseImage, pushImage, pushImage)

	steps := []*cloudbuildpb.BuildStep{
		{
			Id:         "pip-install-via-pypi-mirror",
			Name:       baseImage,
			Entrypoint: "bash",
			Args:       []string{"-c", step1Script},
		},
		{
			Id:         "docker-build-and-push",
			Name:       "gcr.io/cloud-builders/docker",
			Entrypoint: "bash",
			Args:       []string{"-c", step2Script},
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

	dockerHost := fmt.Sprintf("%s-docker.pkg.dev", o.Region)
	baseImage := fmt.Sprintf("%s/%s/%s/library/python:3.12-slim", dockerHost, o.ProjectID, o.DockerHubMirror)

	script := `set -euo pipefail
# 公式 PyPI に直アクセス。FW で 0.0.0.0/0 への egress は deny されているため失敗するはず。
# connect-timeout を短くして高速 fail。
curl --connect-timeout 10 -fsS https://pypi.org/simple/ -o /tmp/idx.html
echo "ERROR: should not reach here"
exit 1
`

	steps := []*cloudbuildpb.BuildStep{
		{
			Id:         "external-access-should-fail",
			Name:       baseImage,
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
