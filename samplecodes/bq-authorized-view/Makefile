.PHONY: init plan apply test destroy all clean

# GCP_PROJECT_ID 環境変数が必須
check-env:
ifndef GCP_PROJECT_ID
	$(error GCP_PROJECT_ID is not set)
endif

init:
	terraform init

plan: check-env
	terraform plan -var="project_id=$(GCP_PROJECT_ID)"

apply: check-env
	terraform apply -auto-approve -var="project_id=$(GCP_PROJECT_ID)"

test: check-env
	go test -v -timeout 300s ./...

destroy: check-env
	terraform destroy -auto-approve -var="project_id=$(GCP_PROJECT_ID)"

# apply → test → destroy を一気に実行
all: apply test destroy

clean: destroy
	rm -rf .terraform terraform.tfstate terraform.tfstate.backup
