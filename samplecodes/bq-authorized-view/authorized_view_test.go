package bq_authorized_view_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"testing"

	"cloud.google.com/go/bigquery"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/impersonate"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
)

// tfOutput は terraform output -json の結果を格納する構造体
type tfOutput struct {
	ProjectID          string
	DatasetA           string
	DatasetB           string
	DatasetC           string
	TableConfidential  string
	ViewPublicDir      string
	ViewNestedDir      string
	User1Email         string
	User2Email         string
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
		ProjectID:          getString("project_id"),
		DatasetA:           getString("dataset_a_id"),
		DatasetB:           getString("dataset_b_id"),
		DatasetC:           getString("dataset_c_id"),
		TableConfidential:  getString("table_confidential_id"),
		ViewPublicDir:      getString("view_public_directory_id"),
		ViewNestedDir:      getString("view_nested_directory_id"),
		User1Email:         getString("user1_email"),
		User2Email:         getString("user2_email"),
	}
}

// newImpersonatedClient は Service Account Impersonation を使って BigQuery クライアントを生成する
func newImpersonatedClient(t *testing.T, ctx context.Context, projectID, targetSA string) *bigquery.Client {
	t.Helper()

	ts, err := impersonate.CredentialsTokenSource(ctx, impersonate.CredentialsConfig{
		TargetPrincipal: targetSA,
		Scopes:          []string{"https://www.googleapis.com/auth/bigquery"},
	})
	if err != nil {
		t.Fatalf("impersonate.CredentialsTokenSource の作成に失敗 (target=%s): %v", targetSA, err)
	}

	client, err := bigquery.NewClient(ctx, projectID, option.WithTokenSource(ts))
	if err != nil {
		t.Fatalf("BigQuery クライアントの作成に失敗 (target=%s): %v", targetSA, err)
	}

	return client
}

// runQuery はクエリを実行し、結果の行数を返す。エラー時はそのまま error を返す。
func runQuery(ctx context.Context, client *bigquery.Client, sql string) (int, error) {
	q := client.Query(sql)
	it, err := q.Read(ctx)
	if err != nil {
		return 0, err
	}

	count := 0
	for {
		var row []bigquery.Value
		err := it.Next(&row)
		if err == iterator.Done {
			break
		}
		if err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

// isPermissionDenied は Google API の 403 エラーかどうかを判定する
func isPermissionDenied(err error) bool {
	if err == nil {
		return false
	}
	if apiErr, ok := err.(*googleapi.Error); ok {
		return apiErr.Code == 403
	}
	return false
}

func TestAuthorizedView(t *testing.T) {
	// 環境変数 GCP_PROJECT_ID でプロジェクト名を注入
	projectID := os.Getenv("GCP_PROJECT_ID")
	if projectID == "" {
		t.Skip("GCP_PROJECT_ID が設定されていないためスキップ")
	}

	tf := getTerraformOutputs(t)
	ctx := context.Background()

	// テストデータの投入（user1 として実行）
	user1Client := newImpersonatedClient(t, ctx, tf.ProjectID, tf.User1Email)
	defer user1Client.Close()

	seedSQL := fmt.Sprintf(
		"INSERT INTO `%s.%s.%s` (id, name, email, salary) SELECT * FROM `%s.%s.confidential_data_seed`",
		tf.ProjectID, tf.DatasetA, tf.TableConfidential,
		tf.ProjectID, tf.DatasetA,
	)
	seedQuery := user1Client.Query(seedSQL)
	job, err := seedQuery.Run(ctx)
	if err != nil {
		t.Fatalf("テストデータ投入クエリの実行に失敗: %v", err)
	}
	status, err := job.Wait(ctx)
	if err != nil {
		t.Fatalf("テストデータ投入ジョブの待機に失敗: %v", err)
	}
	if status.Err() != nil {
		t.Fatalf("テストデータ投入ジョブがエラー: %v", status.Err())
	}

	tests := []struct {
		name    string
		run     func(t *testing.T)
	}{
		{
			name: "正常系：ビュー閲覧 - user2がdataset_bのビューをSELECTできること",
			run: func(t *testing.T) {
				client := newImpersonatedClient(t, ctx, tf.ProjectID, tf.User2Email)
				defer client.Close()

				sql := fmt.Sprintf(
					"SELECT id, name, email FROM `%s.%s.%s`",
					tf.ProjectID, tf.DatasetB, tf.ViewPublicDir,
				)
				count, err := runQuery(ctx, client, sql)
				if err != nil {
					t.Fatalf("ビューの閲覧に失敗: %v", err)
				}
				if count == 0 {
					t.Error("ビューから0件のデータが返されました。テストデータが投入されていない可能性があります")
				}
				t.Logf("ビューから %d 件のデータを取得（dataset_a への直接権限なしで成功）", count)
			},
		},
		{
			name: "異常系：悪意のある更新 - user2がビューSQLを全カラム参照に書き換えられないこと",
			run: func(t *testing.T) {
				client := newImpersonatedClient(t, ctx, tf.ProjectID, tf.User2Email)
				defer client.Close()

				// dataset_a の全カラムを参照するSQLに書き換えようとする
				maliciousSQL := fmt.Sprintf(
					"SELECT * FROM `%s.%s.%s`",
					tf.ProjectID, tf.DatasetA, tf.TableConfidential,
				)

				viewRef := client.Dataset(tf.DatasetB).Table(tf.ViewPublicDir)
				meta, err := viewRef.Metadata(ctx)
				if err != nil {
					t.Fatalf("ビューのメタデータ取得に失敗: %v", err)
				}

				_, err = viewRef.Update(ctx, bigquery.TableMetadataToUpdate{
					ViewQuery: maliciousSQL,
				}, meta.ETag)

				if err == nil {
					t.Fatal("ビューの悪意ある更新が成功してしまいました（403が期待されます）")
				}
				if !isPermissionDenied(err) {
					t.Fatalf("期待されるエラーコード 403 ではありません: %v", err)
				}
				t.Logf("期待通り 403 で拒否されました: %v", err)
			},
		},
		{
			name: "正常系：管理者による更新 - user1がビューSQLを更新し再承認なしで反映されること",
			run: func(t *testing.T) {
				// user1 でビューを更新（カラムを増やす）
				adminClient := newImpersonatedClient(t, ctx, tf.ProjectID, tf.User1Email)
				defer adminClient.Close()

				updatedSQL := fmt.Sprintf(
					"SELECT id, name, email FROM `%s.%s.%s`",
					tf.ProjectID, tf.DatasetA, tf.TableConfidential,
				)

				viewRef := adminClient.Dataset(tf.DatasetB).Table(tf.ViewPublicDir)
				meta, err := viewRef.Metadata(ctx)
				if err != nil {
					t.Fatalf("ビューのメタデータ取得に失敗: %v", err)
				}

				_, err = viewRef.Update(ctx, bigquery.TableMetadataToUpdate{
					ViewQuery: updatedSQL,
				}, meta.ETag)
				if err != nil {
					t.Fatalf("管理者によるビュー更新に失敗: %v", err)
				}

				// user2 で更新後のビューを確認（再承認なしでデータが取得できること）
				user2Client := newImpersonatedClient(t, ctx, tf.ProjectID, tf.User2Email)
				defer user2Client.Close()

				selectSQL := fmt.Sprintf(
					"SELECT id, name, email FROM `%s.%s.%s`",
					tf.ProjectID, tf.DatasetB, tf.ViewPublicDir,
				)
				count, err := runQuery(ctx, user2Client, selectSQL)
				if err != nil {
					t.Fatalf("更新後のビュー閲覧に失敗（再承認が必要になっている可能性）: %v", err)
				}
				if count == 0 {
					t.Error("更新後のビューから0件のデータが返されました")
				}
				t.Logf("再承認なしで更新後のビューから %d 件のデータを取得", count)
			},
		},
		{
			name: "異常系：新規ビュー作成 - user2がdataset_bにdataset_aを参照するビューを作成できないこと",
			run: func(t *testing.T) {
				client := newImpersonatedClient(t, ctx, tf.ProjectID, tf.User2Email)
				defer client.Close()

				maliciousSQL := fmt.Sprintf(
					"SELECT * FROM `%s.%s.%s`",
					tf.ProjectID, tf.DatasetA, tf.TableConfidential,
				)

				viewRef := client.Dataset(tf.DatasetB).Table("malicious_view")
				err := viewRef.Create(ctx, &bigquery.TableMetadata{
					ViewQuery: maliciousSQL,
				})

				if err == nil {
					// 作成が成功してしまった場合、クエリ実行時にブロックされるか確認
					selectSQL := fmt.Sprintf(
						"SELECT * FROM `%s.%s.malicious_view`",
						tf.ProjectID, tf.DatasetB,
					)
					_, queryErr := runQuery(ctx, client, selectSQL)
					if queryErr == nil {
						t.Fatal("不正なビューの作成もクエリ実行も成功してしまいました（権限チェックが機能していません）")
					}
					if !isPermissionDenied(queryErr) {
						t.Fatalf("クエリ実行時に期待されるエラーコード 403 ではありません: %v", queryErr)
					}
					t.Logf("ビュー作成は成功しましたが、クエリ実行時に 403 で拒否されました: %v", queryErr)

					// クリーンアップ
					if delErr := viewRef.Delete(ctx); delErr != nil {
						t.Logf("不正ビューの削除に失敗（手動削除が必要な可能性）: %v", delErr)
					}
					return
				}

				if !isPermissionDenied(err) {
					t.Fatalf("期待されるエラーコード 403 ではありません: %v", err)
				}
				t.Logf("期待通りビュー作成が 403 で拒否されました: %v", err)
			},
		},
		{
			name: "正常系：ネストされた承認 - dataset_cのビューがdataset_bのビューを経由して閲覧できること",
			run: func(t *testing.T) {
				client := newImpersonatedClient(t, ctx, tf.ProjectID, tf.User2Email)
				defer client.Close()

				sql := fmt.Sprintf(
					"SELECT id, name FROM `%s.%s.%s`",
					tf.ProjectID, tf.DatasetC, tf.ViewNestedDir,
				)
				count, err := runQuery(ctx, client, sql)
				if err != nil {
					t.Fatalf("ネストされたビューの閲覧に失敗: %v", err)
				}
				if count == 0 {
					t.Error("ネストされたビューから0件のデータが返されました")
				}
				t.Logf("多段構成のビューから %d 件のデータを取得（中間層 dataset_b の承認のみで成功）", count)
			},
		},
		{
			name: "異常系：ネストのバイパス - user2がdataset_cのビューを編集しdataset_aを直接参照できないこと",
			run: func(t *testing.T) {
				client := newImpersonatedClient(t, ctx, tf.ProjectID, tf.User2Email)
				defer client.Close()

				// 中間層を飛ばして直接 dataset_a を参照するSQLに書き換えようとする
				bypassSQL := fmt.Sprintf(
					"SELECT id, name, email, salary FROM `%s.%s.%s`",
					tf.ProjectID, tf.DatasetA, tf.TableConfidential,
				)

				viewRef := client.Dataset(tf.DatasetC).Table(tf.ViewNestedDir)
				meta, err := viewRef.Metadata(ctx)
				if err != nil {
					t.Fatalf("ビューのメタデータ取得に失敗: %v", err)
				}

				_, err = viewRef.Update(ctx, bigquery.TableMetadataToUpdate{
					ViewQuery: bypassSQL,
				}, meta.ETag)

				if err == nil {
					t.Fatal("ネストバイパスのビュー更新が成功してしまいました（403が期待されます）")
				}
				if !isPermissionDenied(err) {
					t.Fatalf("期待されるエラーコード 403 ではありません: %v", err)
				}
				t.Logf("期待通り 403 で拒否されました（中間層の権限があっても最深部の権限がないため保存不可）: %v", err)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.run(t)
		})
	}
}
