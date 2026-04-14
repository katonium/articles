.PHONY: init plan apply test report destroy all clean

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
	go test -v -timeout 600s ./...

report: check-env
	go test -v -timeout 600s -json ./... | \
	go-test-report -o test-report.html -t "Cloud Build Private Pool 仕様検証レポート"

destroy: check-env
	terraform destroy -auto-approve -var="project_id=$(GOOGLE_CLOUD_PROJECT)"

all: apply report destroy

clean: destroy
	rm -rf .terraform terraform.tfstate terraform.tfstate.backup
