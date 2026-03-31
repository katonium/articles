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

// grantDatasetAccess はデータセットのアクセスリストにエントリを追加し、
// 元に戻すためのクリーンアップ関数を返す。adminClient は dataOwner 権限が必要。
func grantDatasetAccess(
	t *testing.T, ctx context.Context,
	adminClient *bigquery.Client,
	datasetID, saEmail string,
	role bigquery.AccessRole,
) func() {
	t.Helper()

	ds := adminClient.Dataset(datasetID)
	meta, err := ds.Metadata(ctx)
	if err != nil {
		t.Fatalf("データセット %s のメタデータ取得に失敗: %v", datasetID, err)
	}

	originalAccess := make([]*bigquery.AccessEntry, len(meta.Access))
	copy(originalAccess, meta.Access)

	newAccess := append(meta.Access, &bigquery.AccessEntry{
		Role:       role,
		EntityType: bigquery.UserEmailEntity,
		Entity:     saEmail,
	})

	_, err = ds.Update(ctx, bigquery.DatasetMetadataToUpdate{
		Access: newAccess,
	}, meta.ETag)
	if err != nil {
		t.Fatalf("データセット %s へのアクセス付与に失敗: %v", datasetID, err)
	}
	t.Logf("データセット %s に %s (%s) のアクセスを付与", datasetID, saEmail, role)

	return func() {
		restoreMeta, err := ds.Metadata(ctx)
		if err != nil {
			t.Logf("復元用メタデータ取得に失敗: %v", err)
			return
		}
		_, err = ds.Update(ctx, bigquery.DatasetMetadataToUpdate{
			Access: originalAccess,
		}, restoreMeta.ETag)
		if err != nil {
			t.Logf("データセット %s のアクセス復元に失敗（手動で terraform apply が必要な可能性）: %v", datasetID, err)
		}
	}
}

// runDDL はDDL/DMLクエリを実行し完了を待つ
func runDDL(ctx context.Context, client *bigquery.Client, sql string) error {
	q := client.Query(sql)
	job, err := q.Run(ctx)
	if err != nil {
		return err
	}
	status, err := job.Wait(ctx)
	if err != nil {
		return err
	}
	return status.Err()
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
			name: "異常系：直接アクセス拒否 - user2がdataset_aのテーブルを直接SELECTできないこと",
			run: func(t *testing.T) {
				client := newImpersonatedClient(t, ctx, tf.ProjectID, tf.User2Email)
				defer client.Close()

				sql := fmt.Sprintf(
					"SELECT id, name, email, salary FROM `%s.%s.%s`",
					tf.ProjectID, tf.DatasetA, tf.TableConfidential,
				)
				_, err := runQuery(ctx, client, sql)
				if err == nil {
					t.Fatal("dataset_a への直接アクセスが成功してしまいました（403が期待されます）")
				}
				if !isPermissionDenied(err) {
					t.Fatalf("期待されるエラーコード 403 ではありません: %v", err)
				}
				t.Logf("期待通り dataset_a への直接アクセスが 403 で拒否されました: %v", err)
			},
		},
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
			name: "正常系：ビューによるカラム制限 - 承認済みビュー経由ではsalaryカラムにアクセスできないこと",
			run: func(t *testing.T) {
				client := newImpersonatedClient(t, ctx, tf.ProjectID, tf.User2Email)
				defer client.Close()

				// ビューで公開されていない salary カラムを指定してクエリ
				sql := fmt.Sprintf(
					"SELECT salary FROM `%s.%s.%s`",
					tf.ProjectID, tf.DatasetB, tf.ViewPublicDir,
				)
				_, err := runQuery(ctx, client, sql)
				if err == nil {
					t.Fatal("salary カラムへのアクセスが成功してしまいました（ビューで公開されていないカラムです）")
				}
				t.Logf("期待通り salary カラムへのアクセスが拒否されました: %v", err)
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
		{
			name: "異常系：良性編集も拒否 - user2がカラム順序を変えるだけの編集でも参照先の権限がないため拒否されること",
			run: func(t *testing.T) {
				// 承認済みビューは、編集時に参照先データセットへの権限が要求される。
				// 悪意の有無に関わらず、SQLが実際に変更される場合は拒否される。
				// ※ 同一SQLでの更新はBQがno-opとして許可するが、
				//    カラム順序の変更など実質的な変更があると403になる。
				client := newImpersonatedClient(t, ctx, tf.ProjectID, tf.User2Email)
				defer client.Close()

				// カラム順序を変更しただけの良性編集（公開範囲は変わらない）
				reorderedSQL := fmt.Sprintf(
					"SELECT name, email, id FROM `%s.%s.%s`",
					tf.ProjectID, tf.DatasetA, tf.TableConfidential,
				)

				viewRef := client.Dataset(tf.DatasetB).Table(tf.ViewPublicDir)
				meta, err := viewRef.Metadata(ctx)
				if err != nil {
					t.Fatalf("ビューのメタデータ取得に失敗: %v", err)
				}

				_, err = viewRef.Update(ctx, bigquery.TableMetadataToUpdate{
					ViewQuery: reorderedSQL,
				}, meta.ETag)

				if err == nil {
					t.Fatal("カラム順序変更のビュー更新が成功してしまいました（参照先への権限がないため403が期待されます）")
				}
				if !isPermissionDenied(err) {
					t.Fatalf("期待されるエラーコード 403 ではありません: %v", err)
				}
				t.Logf("期待通り良性編集も 403 で拒否されました（参照先 dataset_a への権限がないため）: %v", err)
			},
		},
		{
			name: "異常系：データ編集者によるビュー承認不可 - dataEditorではデータセットの承認済みビューリストを変更できないこと",
			run: func(t *testing.T) {
				// dataEditor ロールには bigquery.datasets.update 権限が含まれないため、
				// データセットのアクセス制御（承認済みビューの追加）を変更できない。
				// 承認済みビューの管理には dataOwner 以上の権限が必要。
				client := newImpersonatedClient(t, ctx, tf.ProjectID, tf.User2Email)
				defer client.Close()

				ds := client.Dataset(tf.DatasetB)
				meta, err := ds.Metadata(ctx)
				if err != nil {
					t.Fatalf("データセットメタデータの取得に失敗: %v", err)
				}

				// 既存のアクセスリストに新しい承認済みビューを追加しようとする
				// （重複を避けるため、既存の承認リストにないビュー名を使用）
				newAccess := append(meta.Access, &bigquery.AccessEntry{
					EntityType: bigquery.ViewEntity,
					View: &bigquery.Table{
						ProjectID: tf.ProjectID,
						DatasetID: tf.DatasetB,
						TableID:   "nonexistent_test_view",
					},
				})

				_, err = ds.Update(ctx, bigquery.DatasetMetadataToUpdate{
					Access: newAccess,
				}, meta.ETag)

				if err == nil {
					t.Fatal("dataEditor でデータセットの承認済みビュー追加が成功してしまいました（bigquery.datasets.update 権限が必要なはず）")
				}
				if !isPermissionDenied(err) {
					t.Fatalf("期待されるエラーコード 403 ではありません: %v", err)
				}
				t.Logf("期待通り dataEditor では承認済みビューの追加が 403 で拒否されました: %v", err)
			},
		},
		{
			name: "正常系：ビュー削除と未承認ビュー再作成 - user2がビューを削除して新規ビューを作成できること",
			run: func(t *testing.T) {
				// 承認済みビューの編集は拒否されるが、削除と新規作成は dataEditor 権限で可能。
				// ただし新規ビューは承認リストに含まれないため、dataset_a を参照するクエリは実行不可。

				// user1 がテスト用ビューを dataset_b に作成
				adminClient := newImpersonatedClient(t, ctx, tf.ProjectID, tf.User1Email)
				defer adminClient.Close()

				tempViewSQL := fmt.Sprintf(
					"SELECT id FROM `%s.%s.%s`",
					tf.ProjectID, tf.DatasetA, tf.TableConfidential,
				)
				tempViewRef := adminClient.Dataset(tf.DatasetB).Table("temp_deletable_view")
				if err := tempViewRef.Create(ctx, &bigquery.TableMetadata{
					ViewQuery: tempViewSQL,
				}); err != nil {
					t.Fatalf("テスト用ビューの作成に失敗: %v", err)
				}

				// user2 がビューを削除（dataEditor 権限で可能）
				user2Client := newImpersonatedClient(t, ctx, tf.ProjectID, tf.User2Email)
				defer user2Client.Close()

				user2TempRef := user2Client.Dataset(tf.DatasetB).Table("temp_deletable_view")
				if err := user2TempRef.Delete(ctx); err != nil {
					t.Fatalf("user2 によるビュー削除に失敗: %v", err)
				}
				t.Log("user2 がビューの削除に成功（dataEditor 権限による）")

				// user2 が新規ビューを作成（dataset_a を参照しないビューは作成可能）
				newViewRef := user2Client.Dataset(tf.DatasetB).Table("user2_new_view")
				if err := newViewRef.Create(ctx, &bigquery.TableMetadata{
					ViewQuery: "SELECT 1 AS id, 'test' AS name",
				}); err != nil {
					t.Fatalf("user2 による新規ビュー作成に失敗: %v", err)
				}
				defer func() {
					if delErr := newViewRef.Delete(ctx); delErr != nil {
						t.Logf("テスト用ビューの削除に失敗: %v", delErr)
					}
				}()
				t.Log("user2 が新規ビュー作成に成功（承認されていないビュー）")
			},
		},
		{
			name: "正常系：多段ビューの前後権限による編集 - 参照先と配置先の権限があればネストされたビューを編集可能",
			run: func(t *testing.T) {
				// 多段承認ビュー（dataset_c → dataset_b → dataset_a）において、
				// dataset_c のビューを編集するには:
				//   - dataset_c への書き込み権限（dataOwner）
				//   - 参照先 dataset_b への datasets.update 権限（dataOwner）
				// が必要。dataset_a への直接権限は不要。
				//
				// user1 は dataset_b, dataset_c の dataOwner だが、
				// ここでは dataset_a を直接参照せず dataset_b のビュー経由で
				// 編集できることを検証する。
				adminClient := newImpersonatedClient(t, ctx, tf.ProjectID, tf.User1Email)
				defer adminClient.Close()

				viewRef := adminClient.Dataset(tf.DatasetC).Table(tf.ViewNestedDir)
				meta, err := viewRef.Metadata(ctx)
				if err != nil {
					t.Fatalf("ネストされたビューのメタデータ取得に失敗: %v", err)
				}

				originalQuery := meta.ViewQuery

				// dataset_b のビューを参照するSQLに更新（カラムのみ変更、参照先は dataset_b のまま）
				updatedSQL := fmt.Sprintf(
					"SELECT id FROM `%s.%s.%s`",
					tf.ProjectID, tf.DatasetB, tf.ViewPublicDir,
				)

				_, err = viewRef.Update(ctx, bigquery.TableMetadataToUpdate{
					ViewQuery: updatedSQL,
				}, meta.ETag)
				if err != nil {
					t.Fatalf("ネストされたビューの編集に失敗: %v", err)
				}
				t.Log("前後のアクセス権限（dataset_c, dataset_b の dataOwner）でネストされたビューを編集成功（dataset_a への権限は不要）")

				// user2 が編集後のビューを閲覧可能であることを確認
				user2Client := newImpersonatedClient(t, ctx, tf.ProjectID, tf.User2Email)
				defer user2Client.Close()

				selectSQL := fmt.Sprintf(
					"SELECT id FROM `%s.%s.%s`",
					tf.ProjectID, tf.DatasetC, tf.ViewNestedDir,
				)
				count, err := runQuery(ctx, user2Client, selectSQL)
				if err != nil {
					t.Fatalf("編集後のネストされたビューの閲覧に失敗: %v", err)
				}
				if count == 0 {
					t.Error("編集後のネストされたビューから0件のデータが返されました")
				}
				t.Logf("編集後のネストされたビューから %d 件のデータを取得", count)

				// 元のSQLに戻す（後続テストへの影響を防ぐ）
				restoreMeta, err := viewRef.Metadata(ctx)
				if err != nil {
					t.Fatalf("復元用メタデータ取得に失敗: %v", err)
				}
				_, err = viewRef.Update(ctx, bigquery.TableMetadataToUpdate{
					ViewQuery: originalQuery,
				}, restoreMeta.ETag)
				if err != nil {
					t.Fatalf("ビューSQLの復元に失敗: %v", err)
				}
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
// 承認済みビューとの違い:
//   - 承認済みビュー: ビュー単位で承認。新規ビューには個別の承認追加が必要。
//   - 承認済みデータセット: データセット全体を承認。将来追加されるビューも自動的に承認される。
//
// 共通の仕様:
//   - どちらの方式でも、ビューを作成・編集するユーザーには参照先テーブルへの
//     bigquery.tables.getData 権限が必要。承認済みデータセットは管理の利便性のための
//     機能であり、ビュー作成者の権限チェックをバイパスするものではない。
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
			name: "異常系：直接アクセス拒否 - user3がdataset_aのテーブルを直接SELECTできないこと",
			run: func(t *testing.T) {
				client := newImpersonatedClient(t, ctx, tf.ProjectID, tf.User3Email)
				defer client.Close()

				sql := fmt.Sprintf(
					"SELECT id, name, email, salary FROM `%s.%s.%s`",
					tf.ProjectID, tf.DatasetA, tf.TableConfidential,
				)
				_, err := runQuery(ctx, client, sql)
				if err == nil {
					t.Fatal("dataset_a への直接アクセスが成功してしまいました（403が期待されます）")
				}
				if !isPermissionDenied(err) {
					t.Fatalf("期待されるエラーコード 403 ではありません: %v", err)
				}
				t.Logf("期待通り dataset_a への直接アクセスが 403 で拒否されました: %v", err)
			},
		},
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
			name: "異常系：新規ビュー作成 - user3がdataset_d内にdataset_aを参照するビューを作成できないこと",
			run: func(t *testing.T) {
				// 承認済みデータセットでも、ビュー作成者には参照先テーブルへの
				// bigquery.tables.getData 権限が必要。user3 は dataset_a への権限を
				// 持たないため、承認済みビューと同様に 403 で拒否される。
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

				if err == nil {
					defer func() {
						if delErr := viewRef.Delete(ctx); delErr != nil {
							t.Logf("テスト用ビューの削除に失敗: %v", delErr)
						}
					}()

					selectSQL := fmt.Sprintf(
						"SELECT * FROM `%s.%s.user3_created_view`",
						tf.ProjectID, tf.DatasetD,
					)
					_, queryErr := runQuery(ctx, client, selectSQL)
					if queryErr == nil {
						t.Fatal("不正なビューの作成もクエリ実行も成功してしまいました（権限チェックが機能していません）")
					}
					if !isPermissionDenied(queryErr) {
						t.Fatalf("クエリ実行時に期待されるエラーコード 403 ではありません: %v", queryErr)
					}
					t.Logf("ビュー作成は成功しましたが、クエリ実行時に 403 で拒否されました: %v", queryErr)
					return
				}

				if !isPermissionDenied(err) {
					t.Fatalf("期待されるエラーコード 403 ではありません: %v", err)
				}
				t.Logf("期待通り 403 で拒否されました（承認済みデータセットでもビュー作成者の権限チェックは行われる）: %v", err)
			},
		},
		{
			name: "異常系：悪意のある更新 - user3がdataset_d内のビューSQLを変更できないこと",
			run: func(t *testing.T) {
				// 承認済みデータセットでも、ビュー更新者には参照先テーブルへの
				// bigquery.tables.getData 権限が必要。承認済みビューと同様に、
				// user3 は dataset_a への権限を持たないため 403 で拒否される。

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

				// user3 でビューSQLを全カラム参照に変更
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

				if err == nil {
					t.Fatal("ビューの悪意ある更新が成功してしまいました（403が期待されます）")
				}
				if !isPermissionDenied(err) {
					t.Fatalf("期待されるエラーコード 403 ではありません: %v", err)
				}
				t.Logf("期待通り 403 で拒否されました（承認済みデータセットでもビュー更新者の権限チェックは行われる）: %v", err)
			},
		},
		{
			name: "正常系：管理者による新規ビュー自動承認 - user1がdataset_d内に新規ビューを作成するとuser3が即座にアクセスできること",
			run: func(t *testing.T) {
				// 承認済みデータセットの最大の特徴: データセット内に新たに作成されたビューは
				// 個別に承認を追加しなくても自動的に承認される。
				// 承認済みビューでは、新規ビューごとに承認の追加が必要。
				adminClient := newImpersonatedClient(t, ctx, tf.ProjectID, tf.User1Email)
				defer adminClient.Close()

				newViewSQL := fmt.Sprintf(
					"SELECT id, name FROM `%s.%s.%s`",
					tf.ProjectID, tf.DatasetA, tf.TableConfidential,
				)

				viewRef := adminClient.Dataset(tf.DatasetD).Table("auto_authorized_view")
				err := viewRef.Create(ctx, &bigquery.TableMetadata{
					ViewQuery: newViewSQL,
				})
				if err != nil {
					t.Fatalf("管理者による新規ビュー作成に失敗: %v", err)
				}
				defer func() {
					if delErr := viewRef.Delete(ctx); delErr != nil {
						t.Logf("テスト用ビューの削除に失敗: %v", delErr)
					}
				}()

				// user3 が新規ビューに即座にアクセスできることを確認
				user3Client := newImpersonatedClient(t, ctx, tf.ProjectID, tf.User3Email)
				defer user3Client.Close()

				selectSQL := fmt.Sprintf(
					"SELECT id, name FROM `%s.%s.auto_authorized_view`",
					tf.ProjectID, tf.DatasetD,
				)
				count, err := runQuery(ctx, user3Client, selectSQL)
				if err != nil {
					t.Fatalf("新規ビューへのアクセスに失敗（承認済みデータセットでは自動承認されるはず）: %v", err)
				}
				if count == 0 {
					t.Error("新規ビューから0件のデータが返されました")
				}
				t.Logf("承認済みデータセット内の新規ビューに個別承認なしで %d 件のデータを取得（自動承認の確認）", count)
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
		{
			name: "正常系：SELECT_STARビューのカラム自動追従 - カラム追加後にSELECT *ビューで新カラムが参照可能になること",
			run: func(t *testing.T) {
				// SELECT * で定義したビューは、元テーブルにカラムが追加されると
				// 自動的に新カラムも参照可能になる。
				adminClient := newImpersonatedClient(t, ctx, tf.ProjectID, tf.User1Email)
				defer adminClient.Close()

				// dataset_d に SELECT * ビューを作成（承認済みデータセットで自動承認）
				starViewSQL := fmt.Sprintf(
					"SELECT * FROM `%s.%s.%s`",
					tf.ProjectID, tf.DatasetA, tf.TableConfidential,
				)
				viewRef := adminClient.Dataset(tf.DatasetD).Table("star_view_test")
				if err := viewRef.Create(ctx, &bigquery.TableMetadata{
					ViewQuery: starViewSQL,
				}); err != nil {
					t.Fatalf("SELECT * ビューの作成に失敗: %v", err)
				}
				defer func() {
					if delErr := viewRef.Delete(ctx); delErr != nil {
						t.Logf("テスト用ビューの削除に失敗: %v", delErr)
					}
				}()

				// 元テーブルにカラムを追加
				alterSQL := fmt.Sprintf(
					"ALTER TABLE `%s.%s.%s` ADD COLUMN department STRING",
					tf.ProjectID, tf.DatasetA, tf.TableConfidential,
				)
				if err := runDDL(ctx, adminClient, alterSQL); err != nil {
					t.Fatalf("カラム追加に失敗: %v", err)
				}
				defer func() {
					dropSQL := fmt.Sprintf(
						"ALTER TABLE `%s.%s.%s` DROP COLUMN department",
						tf.ProjectID, tf.DatasetA, tf.TableConfidential,
					)
					if err := runDDL(ctx, adminClient, dropSQL); err != nil {
						t.Logf("カラム削除に失敗（手動で削除が必要）: %v", err)
					}
				}()

				// user3 で SELECT * ビューから新カラムを参照
				user3Client := newImpersonatedClient(t, ctx, tf.ProjectID, tf.User3Email)
				defer user3Client.Close()

				selectSQL := fmt.Sprintf(
					"SELECT department FROM `%s.%s.star_view_test`",
					tf.ProjectID, tf.DatasetD,
				)
				_, err := runQuery(ctx, user3Client, selectSQL)
				if err != nil {
					t.Fatalf("SELECT * ビューから追加カラムの参照に失敗（自動追従されるはず）: %v", err)
				}
				t.Log("SELECT * ビューでカラム追加が自動追従されることを確認")
			},
		},
		{
			name: "異常系：明示カラムビューの非追従 - カラム追加後も明示カラムビューでは新カラムが参照不可であること",
			run: func(t *testing.T) {
				// SELECT col_a, col_b, ... で定義したビューは、元テーブルにカラムが
				// 追加されてもビューのスキーマは変わらず、新カラムは参照できない。
				adminClient := newImpersonatedClient(t, ctx, tf.ProjectID, tf.User1Email)
				defer adminClient.Close()

				// dataset_d に明示カラムビューを作成
				explicitViewSQL := fmt.Sprintf(
					"SELECT id, name, email FROM `%s.%s.%s`",
					tf.ProjectID, tf.DatasetA, tf.TableConfidential,
				)
				viewRef := adminClient.Dataset(tf.DatasetD).Table("explicit_view_test")
				if err := viewRef.Create(ctx, &bigquery.TableMetadata{
					ViewQuery: explicitViewSQL,
				}); err != nil {
					t.Fatalf("明示カラムビューの作成に失敗: %v", err)
				}
				defer func() {
					if delErr := viewRef.Delete(ctx); delErr != nil {
						t.Logf("テスト用ビューの削除に失敗: %v", delErr)
					}
				}()

				// 元テーブルにカラムを追加
				alterSQL := fmt.Sprintf(
					"ALTER TABLE `%s.%s.%s` ADD COLUMN department STRING",
					tf.ProjectID, tf.DatasetA, tf.TableConfidential,
				)
				if err := runDDL(ctx, adminClient, alterSQL); err != nil {
					t.Fatalf("カラム追加に失敗: %v", err)
				}
				defer func() {
					dropSQL := fmt.Sprintf(
						"ALTER TABLE `%s.%s.%s` DROP COLUMN department",
						tf.ProjectID, tf.DatasetA, tf.TableConfidential,
					)
					if err := runDDL(ctx, adminClient, dropSQL); err != nil {
						t.Logf("カラム削除に失敗（手動で削除が必要）: %v", err)
					}
				}()

				// user3 で明示カラムビューから新カラムを参照 → 失敗するはず
				user3Client := newImpersonatedClient(t, ctx, tf.ProjectID, tf.User3Email)
				defer user3Client.Close()

				selectSQL := fmt.Sprintf(
					"SELECT department FROM `%s.%s.explicit_view_test`",
					tf.ProjectID, tf.DatasetD,
				)
				_, err := runQuery(ctx, user3Client, selectSQL)
				if err == nil {
					t.Fatal("明示カラムビューから追加カラムが参照できてしまいました（ビュー定義に含まれないカラムは参照不可が期待されます）")
				}
				t.Logf("期待通り明示カラムビューでは追加カラムが参照不可: %v", err)
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
// 承認済みビュー編集に必要な最小権限の検証
//
// ビューの編集には以下の権限が関係する:
//   - ビュー配置先データセット（dataset_b）: bigquery.tables.update
//   - 参照先データセット（dataset_a）: bigquery.tables.getData
//
// 各ロールに含まれる権限:
//   - dataViewer (ReaderRole): tables.getData ✓, tables.update ✗
//   - dataEditor (WriterRole): tables.getData ✓, tables.update ✓
//   - dataOwner  (OwnerRole): tables.getData ✓, tables.update ✓, datasets.update ✓
//
// user2 のベースライン権限:
//   - dataset_a: なし（Terraform 管理）
//   - dataset_b: dataViewer + dataEditor（Terraform 管理）
//
// テスト中に BigQuery API で dataset_a への権限を一時付与して検証する。
// ══════════════════════════════════════════════

func TestAuthorizedViewEditPermissions(t *testing.T) {
	projectID := os.Getenv("GOOGLE_CLOUD_PROJECT")
	if projectID == "" {
		t.Skip("GOOGLE_CLOUD_PROJECT が設定されていないためスキップ")
	}

	tf := getTerraformOutputs(t)
	ctx := context.Background()
	seedTestData(t, ctx, tf)

	adminClient := newImpersonatedClient(t, ctx, tf.ProjectID, tf.User1Email)
	defer adminClient.Close()

	// 元のビュー SQL を取得（各テスト後の復元用）
	viewRef := adminClient.Dataset(tf.DatasetB).Table(tf.ViewPublicDir)
	originalMeta, err := viewRef.Metadata(ctx)
	if err != nil {
		t.Fatalf("ビューのメタデータ取得に失敗: %v", err)
	}
	originalSQL := originalMeta.ViewQuery

	// ビュー SQL を復元するヘルパー
	restoreViewSQL := func(t *testing.T) {
		t.Helper()
		meta, err := viewRef.Metadata(ctx)
		if err != nil {
			t.Fatalf("復元用メタデータ取得に失敗: %v", err)
		}
		if meta.ViewQuery == originalSQL {
			return
		}
		_, err = viewRef.Update(ctx, bigquery.TableMetadataToUpdate{
			ViewQuery: originalSQL,
		}, meta.ETag)
		if err != nil {
			t.Fatalf("ビュー SQL の復元に失敗: %v", err)
		}
	}

	// 編集テストに使うSQL（カラム順序を変更する良性編集）
	editedSQL := fmt.Sprintf(
		"SELECT name, email, id FROM `%s.%s.%s`",
		tf.ProjectID, tf.DatasetA, tf.TableConfidential,
	)

	tests := []struct {
		name     string
		grantOnA bigquery.AccessRole // dataset_a に付与するロール
		grantOnB bigquery.AccessRole // dataset_b に追加付与するロール（空なら追加なし）
	}{
		{
			name:     "dataEditor(B) + dataViewer(A)",
			grantOnA: bigquery.ReaderRole,
		},
		{
			name:     "dataEditor(B) + dataEditor(A)",
			grantOnA: bigquery.WriterRole,
		},
		{
			name:     "dataOwner(B) + dataViewer(A)",
			grantOnA: bigquery.ReaderRole,
			grantOnB: bigquery.OwnerRole,
		},
		{
			name:     "dataEditor(B) + dataOwner(A)",
			grantOnA: bigquery.OwnerRole,
		},
		{
			name:     "dataOwner(B) + dataOwner(A)",
			grantOnA: bigquery.OwnerRole,
			grantOnB: bigquery.OwnerRole,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// dataset_a に一時的にアクセスを付与
			cleanupA := grantDatasetAccess(t, ctx, adminClient, tf.DatasetA, tf.User2Email, tt.grantOnA)
			defer cleanupA()

			// dataset_b に追加ロールを付与（指定がある場合）
			if tt.grantOnB != "" {
				cleanupB := grantDatasetAccess(t, ctx, adminClient, tf.DatasetB, tf.User2Email, tt.grantOnB)
				defer cleanupB()
			}

			// user2 でビュー編集を試みる
			user2Client := newImpersonatedClient(t, ctx, tf.ProjectID, tf.User2Email)
			defer user2Client.Close()

			user2ViewRef := user2Client.Dataset(tf.DatasetB).Table(tf.ViewPublicDir)
			meta, err := user2ViewRef.Metadata(ctx)
			if err != nil {
				t.Fatalf("user2 によるビューメタデータ取得に失敗: %v", err)
			}

			_, err = user2ViewRef.Update(ctx, bigquery.TableMetadataToUpdate{
				ViewQuery: editedSQL,
			}, meta.ETag)

			if err == nil {
				t.Log("✅ ビュー編集に成功")
				restoreViewSQL(t)
			} else if isPermissionDenied(err) {
				t.Logf("❌ 403 Permission Denied: %v", err)
			} else {
				t.Logf("❌ その他のエラー: %v", err)
			}
		})
	}
}
