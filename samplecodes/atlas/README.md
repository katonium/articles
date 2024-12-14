
https://atlasgo.io/getting-started/

### Install Terraform
https://developer.hashicorp.com/terraform/install#linux
```
wget -O- https://apt.releases.hashicorp.com/gpg | sudo gpg --dearmor -o /usr/share/keyrings/hashicorp-archive-keyring.gpg
echo "deb [signed-by=/usr/share/keyrings/hashicorp-archive-keyring.gpg] https://apt.releases.hashicorp.com $(lsb_release -cs) main" | sudo tee /etc/apt/sources.list.d/hashicorp.list
sudo apt update && sudo apt install terraform
```

```
$ terraform -v
Terraform v1.7.5
on linux_amd64
```

### Install Atlas

```
$ curl -sSf https://atlasgo.sh | sh
Install 'atlas-linux-amd64-latest' to '/usr/local/bin/atlas'? [y/N] y
Downloading https://release.ariga.io/atlas/atlas-linux-amd64-latest
######################################################################## 100.0%
atlas version v0.20.1-a4257be-canary
https://github.com/ariga/atlas/releases/latest
Installation successful!
```

```
atlas version
atlas version v0.20.1-a4257be-canary
https://github.com/ariga/atlas/releases/latest
```

