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
	ProjectID           string
	DatasetA            string
	DatasetB            string
	DatasetC            string
	DatasetD            string
	DatasetE            string
	TableConfidential   string
	ViewPublicDir       string
	ViewNestedDir       string
	ViewDSPublicDir     string
	ViewDSNestedDir     string
	User1Email          string
	User2Email          string
	User3Email          string
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
		ProjectID:           getString("project_id"),
		DatasetA:            getString("dataset_a_id"),
		DatasetB:            getString("dataset_b_id"),
		DatasetC:            getString("dataset_c_id"),
		DatasetD:            getString("dataset_d_id"),
		DatasetE:            getString("dataset_e_id"),
		TableConfidential:   getString("table_confidential_id"),
		ViewPublicDir:       getString("view_public_directory_id"),
		ViewNestedDir:       getString("view_nested_directory_id"),
		ViewDSPublicDir:     getString("view_ds_public_directory_id"),
		ViewDSNestedDir:     getString("view_ds_nested_directory_id"),
		User1Email:          getString("user1_email"),
		User2Email:          getString("user2_email"),
		User3Email:          getString("user3_email"),
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

// seedTestData はテストデータを投入する
func seedTestData(t *testing.T, ctx context.Context, tf *tfOutput) {
	t.Helper()

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
}

// ══════════════════════════════════════════════
// 承認済みビュー（Authorized View）のテスト
// ══════════════════════════════════════════════

func TestAuthorizedView(t *testing.T) {
	projectID := os.Getenv("GOOGLE_CLOUD_PROJECT")
	if projectID == "" {
		t.Skip("GOOGLE_CLOUD_PROJECT が設定されていないためスキップ")
	}

	tf := getTerraformOutputs(t)
	ctx := context.Background()
	seedTestData(t, ctx, tf)

	tests := []struct {
		name string
		run  func(t *testing.T)
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

// ══════════════════════════════════════════════
// 承認済みデータセット（Authorized Dataset）のテスト
//
// 承認済みビューとの決定的な違い:
//   - 承認済みビュー: ビュー単位で承認。編集者がソースデータへの権限を持つ必要がある。
//   - 承認済みデータセット: データセット全体を承認。そのデータセット内なら
//     誰でもビューを作成・編集してソースにアクセスできる。
//
// このテストでは、承認済みデータセットのこの「広い信頼モデル」を検証する。
// ══════════════════════════════════════════════

func TestAuthorizedDataset(t *testing.T) {
	projectID := os.Getenv("GOOGLE_CLOUD_PROJECT")
	if projectID == "" {
		t.Skip("GOOGLE_CLOUD_PROJECT が設定されていないためスキップ")
	}

	tf := getTerraformOutputs(t)
	ctx := context.Background()
	seedTestData(t, ctx, tf)

	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "正常系：承認済みデータセット経由の閲覧 - user3がdataset_dのビューからdataset_aのデータを取得できること",
			run: func(t *testing.T) {
				client := newImpersonatedClient(t, ctx, tf.ProjectID, tf.User3Email)
				defer client.Close()

				sql := fmt.Sprintf(
					"SELECT id, name, email FROM `%s.%s.%s`",
					tf.ProjectID, tf.DatasetD, tf.ViewDSPublicDir,
				)
				count, err := runQuery(ctx, client, sql)
				if err != nil {
					t.Fatalf("承認済みデータセットのビュー閲覧に失敗: %v", err)
				}
				if count == 0 {
					t.Error("ビューから0件のデータが返されました")
				}
				t.Logf("承認済みデータセット経由で %d 件のデータを取得（dataset_a への直接権限なしで成功）", count)
			},
		},
		{
			name: "正常系（承認済みビューとの違い）：user3がdataset_d内に新規ビューを作成してdataset_aを参照できること",
			run: func(t *testing.T) {
				// 承認済みビューでは user2 の新規ビュー作成は 403 で拒否された。
				// 承認済みデータセットでは、データセット全体が承認されているため、
				// dataset_d 内に新しいビューを自由に作成して dataset_a を参照できる。
				client := newImpersonatedClient(t, ctx, tf.ProjectID, tf.User3Email)
				defer client.Close()

				newViewSQL := fmt.Sprintf(
					"SELECT id, name, email, salary FROM `%s.%s.%s`",
					tf.ProjectID, tf.DatasetA, tf.TableConfidential,
				)

				viewRef := client.Dataset(tf.DatasetD).Table("user3_created_view")
				err := viewRef.Create(ctx, &bigquery.TableMetadata{
					ViewQuery: newViewSQL,
				})
				if err != nil {
					t.Fatalf("承認済みデータセット内でのビュー作成に失敗: %v", err)
				}
				defer func() {
					if delErr := viewRef.Delete(ctx); delErr != nil {
						t.Logf("テスト用ビューの削除に失敗: %v", delErr)
					}
				}()

				// 作成したビューでクエリを実行（salary を含む全カラムが取得できる）
				selectSQL := fmt.Sprintf(
					"SELECT id, name, email, salary FROM `%s.%s.user3_created_view`",
					tf.ProjectID, tf.DatasetD,
				)
				count, err := runQuery(ctx, client, selectSQL)
				if err != nil {
					t.Fatalf("新規作成ビューのクエリ実行に失敗: %v", err)
				}
				if count == 0 {
					t.Error("新規作成ビューから0件のデータが返されました")
				}
				t.Logf("承認済みデータセット内で user3 が作成したビューから %d 件取得（salary含む全カラム）。"+
					"承認済みビューとの重要な違い: データセット全体が信頼されているため、ビュー作成が可能", count)
			},
		},
		{
			name: "正常系（承認済みビューとの違い）：user3がdataset_d内のビューSQLを自由に変更できること",
			run: func(t *testing.T) {
				// 承認済みビューでは user2 がビューSQLを変更しようとすると 403 で拒否された。
				// 承認済みデータセットでは、user3 は dataset_a への直接権限がなくても、
				// dataset_d 内のビューSQLを自由に変更できる。

				// まず user1 でテスト用ビューを作成
				adminClient := newImpersonatedClient(t, ctx, tf.ProjectID, tf.User1Email)
				defer adminClient.Close()

				initialSQL := fmt.Sprintf(
					"SELECT id, name FROM `%s.%s.%s`",
					tf.ProjectID, tf.DatasetA, tf.TableConfidential,
				)
				testViewRef := adminClient.Dataset(tf.DatasetD).Table("user3_editable_view")
				err := testViewRef.Create(ctx, &bigquery.TableMetadata{
					ViewQuery: initialSQL,
				})
				if err != nil {
					t.Fatalf("テスト用ビューの作成に失敗: %v", err)
				}
				defer func() {
					if delErr := testViewRef.Delete(ctx); delErr != nil {
						t.Logf("テスト用ビューの削除に失敗: %v", delErr)
					}
				}()

				// user3 でビューSQLを全カラム参照に変更（承認済みビューでは 403 になるパターン）
				user3Client := newImpersonatedClient(t, ctx, tf.ProjectID, tf.User3Email)
				defer user3Client.Close()

				expandedSQL := fmt.Sprintf(
					"SELECT id, name, email, salary FROM `%s.%s.%s`",
					tf.ProjectID, tf.DatasetA, tf.TableConfidential,
				)

				user3ViewRef := user3Client.Dataset(tf.DatasetD).Table("user3_editable_view")
				meta, err := user3ViewRef.Metadata(ctx)
				if err != nil {
					t.Fatalf("ビューのメタデータ取得に失敗: %v", err)
				}

				_, err = user3ViewRef.Update(ctx, bigquery.TableMetadataToUpdate{
					ViewQuery: expandedSQL,
				}, meta.ETag)
				if err != nil {
					t.Fatalf("承認済みデータセット内でのビューSQL変更に失敗: %v（承認済みビューとは異なり成功するはず）", err)
				}

				// 変更後のビューで salary を含むクエリが成功することを確認
				selectSQL := fmt.Sprintf(
					"SELECT id, name, email, salary FROM `%s.%s.user3_editable_view`",
					tf.ProjectID, tf.DatasetD,
				)
				count, err := runQuery(ctx, user3Client, selectSQL)
				if err != nil {
					t.Fatalf("変更後のビュークエリ実行に失敗: %v", err)
				}
				if count == 0 {
					t.Error("変更後のビューから0件のデータが返されました")
				}
				t.Logf("user3 がビューSQLを全カラム参照に変更し %d 件取得成功。"+
					"承認済みビューでは 403 になる操作が、承認済みデータセットでは成功する", count)
			},
		},
		{
			name: "異常系：承認済みデータセットの範囲外アクセス - user3がdataset_d内のビューで未承認のデータセットを参照できないこと",
			run: func(t *testing.T) {
				// dataset_d は dataset_a のみ承認されている。
				// dataset_b は承認されていないため、dataset_d のビューから dataset_b を参照しようとすると失敗する。
				client := newImpersonatedClient(t, ctx, tf.ProjectID, tf.User3Email)
				defer client.Close()

				// dataset_b の承認済みビューを参照しようとする（dataset_d → dataset_b は未承認）
				unauthorizedSQL := fmt.Sprintf(
					"SELECT id, name, email FROM `%s.%s.%s`",
					tf.ProjectID, tf.DatasetB, tf.ViewPublicDir,
				)

				viewRef := client.Dataset(tf.DatasetD).Table("unauthorized_ref_view")
				err := viewRef.Create(ctx, &bigquery.TableMetadata{
					ViewQuery: unauthorizedSQL,
				})

				if err == nil {
					// ビュー作成自体は成功する場合がある（参照先の検証はクエリ時）
					selectSQL := fmt.Sprintf(
						"SELECT * FROM `%s.%s.unauthorized_ref_view`",
						tf.ProjectID, tf.DatasetD,
					)
					_, queryErr := runQuery(ctx, client, selectSQL)
					if queryErr == nil {
						t.Fatal("未承認データセットへの参照が成功してしまいました")
					}
					if !isPermissionDenied(queryErr) {
						t.Fatalf("期待されるエラーコード 403 ではありません: %v", queryErr)
					}
					t.Logf("ビュー作成は成功しましたが、クエリ実行時に 403 で拒否されました: %v", queryErr)

					if delErr := viewRef.Delete(ctx); delErr != nil {
						t.Logf("テスト用ビューの削除に失敗: %v", delErr)
					}
					return
				}

				if !isPermissionDenied(err) {
					t.Fatalf("期待されるエラーコード 403 ではありません: %v", err)
				}
				t.Logf("期待通り 403 で拒否されました（dataset_d は dataset_b に対して承認されていない）: %v", err)
			},
		},
		{
			name: "正常系：承認済みデータセットのネスト - dataset_eのビューがdataset_dのビューを経由して閲覧できること",
			run: func(t *testing.T) {
				client := newImpersonatedClient(t, ctx, tf.ProjectID, tf.User3Email)
				defer client.Close()

				sql := fmt.Sprintf(
					"SELECT id, name FROM `%s.%s.%s`",
					tf.ProjectID, tf.DatasetE, tf.ViewDSNestedDir,
				)
				count, err := runQuery(ctx, client, sql)
				if err != nil {
					t.Fatalf("承認済みデータセットのネストされたビュー閲覧に失敗: %v", err)
				}
				if count == 0 {
					t.Error("ネストされたビューから0件のデータが返されました")
				}
				t.Logf("承認済みデータセットの多段構成で %d 件のデータを取得", count)
			},
		},
		{
			name: "異常系：承認済みデータセットのネストバイパス - user3がdataset_eのビューで中間層を飛ばしてdataset_aを直接参照できないこと",
			run: func(t *testing.T) {
				// dataset_e は dataset_d に対して承認済みだが、dataset_a に対しては直接承認されていない。
				// dataset_e のビューから直接 dataset_a を参照しようとすると失敗するはず。
				client := newImpersonatedClient(t, ctx, tf.ProjectID, tf.User3Email)
				defer client.Close()

				bypassSQL := fmt.Sprintf(
					"SELECT id, name, email, salary FROM `%s.%s.%s`",
					tf.ProjectID, tf.DatasetA, tf.TableConfidential,
				)

				viewRef := client.Dataset(tf.DatasetE).Table(tf.ViewDSNestedDir)
				meta, err := viewRef.Metadata(ctx)
				if err != nil {
					t.Fatalf("ビューのメタデータ取得に失敗: %v", err)
				}

				_, err = viewRef.Update(ctx, bigquery.TableMetadataToUpdate{
					ViewQuery: bypassSQL,
				}, meta.ETag)

				if err == nil {
					// 更新が成功した場合、クエリ時に拒否されるか確認
					selectSQL := fmt.Sprintf(
						"SELECT * FROM `%s.%s.%s`",
						tf.ProjectID, tf.DatasetE, tf.ViewDSNestedDir,
					)
					_, queryErr := runQuery(ctx, client, selectSQL)
					if queryErr == nil {
						t.Fatal("ネストバイパスが完全に成功してしまいました（dataset_e → dataset_a の直接参照がブロックされるべき）")
					}
					t.Logf("ビュー更新は成功しましたが、クエリ実行時に拒否されました: %v", queryErr)
					return
				}

				if !isPermissionDenied(err) {
					t.Fatalf("期待されるエラーコード 403 ではありません: %v", err)
				}
				t.Logf("期待通り 403 で拒否されました（dataset_e は dataset_a に対して直接承認されていない）: %v", err)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.run(t)
		})
	}
}
