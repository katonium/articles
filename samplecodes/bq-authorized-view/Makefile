.PHONY: init plan apply test destroy all clean

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

destroy: check-env
	terraform destroy -auto-approve -var="project_id=$(GOOGLE_CLOUD_PROJECT)"

# apply → test → destroy を一気に実行
all: apply test destroy

clean: destroy
	rm -rf .terraform terraform.tfstate terraform.tfstate.backup
