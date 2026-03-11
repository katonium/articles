.PHONY: init plan apply test report destroy all clean

# GOOGLE_CLOUD_PROJECT 環境変数が必須
check-env:
ifndef GOOGLE_CLOUD_PROJECT
	$(error GOOGLE_CLOUD_PROJECT is not set)
endif

init:
	terraform init

plan: check-env
	terraform plan -var="project_id=$(GOOGLE_CLOUD_PROJECT)"

apply: check-env
	terraform apply -auto-approve -var="project_id=$(GOOGLE_CLOUD_PROJECT)"

test: check-env
	go test -v -timeout 300s ./...

report: check-env
	go test -v -timeout 300s -json ./... | go-test-report -o test-report.html -t "BigQuery 承認済みビュー/データセット 仕様検証レポート"

destroy: check-env
	terraform destroy -auto-approve -var="project_id=$(GOOGLE_CLOUD_PROJECT)"

# apply → test（レポート付き） → destroy を一気に実行
all: apply report destroy

clean: destroy
	rm -rf .terraform terraform.tfstate terraform.tfstate.backup
