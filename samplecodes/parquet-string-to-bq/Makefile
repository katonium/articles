.PHONY: test check-env

# GOOGLE_CLOUD_PROJECT 環境変数が必須 (未指定なら ADC から自動検出を試みる)
check-env:
ifndef GOOGLE_CLOUD_PROJECT
	@echo "WARN: GOOGLE_CLOUD_PROJECT 未設定。ADC / gcloud のデフォルトプロジェクトを使用します"
endif

# データセット/テーブルはテスト内で作成・破棄するため、事前の terraform apply は不要。
# 必要な権限: BigQuery Job User + データセット作成 (bigquery.datasets.create) 権限。
test: check-env
	go test -v -timeout 600s ./...
