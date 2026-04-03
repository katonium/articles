# AI時代のクラウドネイティブ - Research Notes

> 15-minute talk preparation

## 1. Cloud Native Definitions - Comparison Across Organizations

### 1.1 CNCF (Cloud Native Computing Foundation)

CNCFはクラウドネイティブの最も権威ある定義を提供しており、v1.0からv1.1へ改訂されている。

#### v1.0 (Original - CNCF Charter)

> Cloud native technologies empower organizations to build and run scalable applications in modern, dynamic environments such as public, private, and hybrid clouds. Containers, service meshes, microservices, immutable infrastructure, and declarative APIs exemplify this approach.
>
> These techniques enable loosely coupled systems that are resilient, manageable, and observable. Combined with robust automation, they allow engineers to make high-impact changes frequently and predictably with minimal toil.

- **Ref**: [CNCF - Who We Are](https://www.cncf.io/about/who-we-are/)

#### v1.1 (Updated - Approved February 26, 2024)

> Cloud native practices empower organizations to develop, build, and deploy workloads in computing environments (public, private, hybrid cloud) to meet their organizational needs at scale in a programmatic and repeatable manner.
>
> It is characterized by loosely coupled systems that interoperate in a manner that is secure, resilient, manageable, sustainable, and observable.
>
> Cloud native technologies and architectures typically consist of some combination of containers, service meshes, multi-tenancy, microservices, immutable infrastructure, serverless, and declarative APIs — this list is non-exhaustive.

- **Ref**: [CNCF Cloud Native Definition v1.1 (GitHub)](https://github.com/cncf/toc/blob/main/DEFINITION.md)

#### v1.0 → v1.1 Key Changes

| Aspect | v1.0 | v1.1 |
|---|---|---|
| Subject | "technologies" | "practices" (broader scope) |
| Qualities | resilient, manageable, observable | + **secure**, **sustainable** |
| Technologies | containers, service meshes, microservices, immutable infrastructure, declarative APIs | + **multi-tenancy**, **serverless**; "non-exhaustive" |
| Actor | "engineers" | "organizations" |
| Goal | "make high-impact changes" | + "clear separation of concerns" |

---

### 1.2 Google Cloud

> Cloud native is an approach to building and running scalable applications to take full advantage of cloud-based services and delivery models.

Google Cloudは5つのコアピラーを定義:

1. **Microservices** - 小さな軽量サービスに分割し、API経由で接続
2. **DevOps** - 開発とIT運用の協業によるインフラ・デリバリー自動化
3. **CI/CD** - ビルド・テスト・デプロイの自動化パイプライン
4. **Containers** - アプリケーションコンポーネントの軽量パッケージング
5. **Declarative APIs** - 宣言的なインターフェース定義

また、Well-Architectedなクラウドネイティブシステムの特徴として「self-healing（自己修復）」「cost efficient（コスト効率的）」「CI/CDによる容易な更新・保守」を挙げている。

- **Ref**: [What Is Cloud Native | Google Cloud](https://cloud.google.com/learn/what-is-cloud-native)
- **Ref**: [5 Principles for Cloud-Native Architecture | Google Cloud Blog](https://cloud.google.com/blog/products/application-development/5-principles-for-cloud-native-architecture-what-it-is-and-how-to-master-it)

---

### 1.3 AWS (Amazon Web Services)

> Cloud native is the software approach of building, deploying, and managing modern applications in cloud computing environments. Modern companies want to build highly scalable, flexible, and resilient applications that they can update quickly to meet customer demands.

AWSの特徴的な視点:
- **コスト削減**: 物理インフラの調達・保守不要による長期的なコスト削減を強調
- **連続体（Continuum）としてのクラウドネイティブ**: AWS Partner Network Blogは、厳密な定義よりも「Cloud Native Maturity Model」としての連続体の概念を提唱。"cloud-native services", "application-centric design", "automation" を段階的に進化するコア要素としている
- **マイクロサービス・アジャイル・DevOps**: 従来のモノリシックから分離された小サービスへの変革

- **Ref**: [What is Cloud Native? | AWS](https://aws.amazon.com/what-is/cloud-native/)
- **Ref**: [Journey to Being Cloud-Native | AWS Partner Network Blog](https://aws.amazon.com/blogs/apn/journey-to-being-cloud-native-how-and-where-should-you-start/)

---

### 1.4 Oracle

> Cloud native is an approach to building and running applications that leverages cloud computing technologies. It includes using cloud-native technologies and practices to design, develop, and deploy scalable, resilient, and agile applications.

Oracleは独自の定義を持ちつつも、CNCFの定義を明示的に参照している。Cloud Adoption Frameworkでは以下を強調:

- **コンテナ、マイクロサービス、サーバーレスアーキテクチャ**の活用
- **DevOpsプラクティス**によるソフトウェアデリバリーの自動化・効率化
- **Oracle Cloud Native Environment (CNE)**: OCI・CNCFの標準仕様に基づくオープンソースプロジェクトのキュレーションセット

- **Ref**: [Cloud Native - Oracle Cloud Adoption Framework](https://docs.oracle.com/en-us/iaas/Content/cloud-adoption-framework/cloud-native.htm)
- **Ref**: [What is Cloud Native | Oracle](https://www.oracle.com/cloud/cloud-native/what-is-cloud-native/)

---

### 1.5 IBM

> Cloud native refers less to where an application resides and more to how it is built and deployed.

IBMの特徴的な視点:
- **「どこで動くか」ではなく「どう作り・デプロイするか」**: クラウドネイティブの本質はインフラではなく手法にあると強調
- **Cloud Native vs Cloud Enabled の明確な区別**: "Cloud-enabled" はオンプレ向けに開発されたものを後からクラウド対応させたもの。"Cloud-native" はクラウドでのみ動作するよう最初から設計されたもの
- **マイクロサービス = ビルディングブロック**: コンテナにパッケージされた再利用可能なコンポーネントがアプリケーション全体を構成

- **Ref**: [What Is Cloud Native? | IBM](https://www.ibm.com/think/topics/cloud-native)

---

## 2. Cross-Cutting Summary: Common Themes

All definitions share these core elements:

| Theme | CNCF | Google | AWS | Oracle | IBM |
|---|---|---|---|---|---|
| Microservices | ✅ | ✅ | ✅ | ✅ | ✅ |
| Containers | ✅ | ✅ | ✅ | ✅ | ✅ |
| DevOps / CI/CD | ✅ (automation) | ✅ | ✅ | ✅ | ✅ |
| Declarative APIs | ✅ | ✅ | - | - | - |
| Scalability | ✅ | ✅ | ✅ | ✅ | ✅ |
| Resilience | ✅ | ✅ | ✅ | ✅ | - |
| Loosely coupled | ✅ | ✅ | - | ✅ | ✅ |
| Serverless | ✅ (v1.1) | - | ✅ | ✅ | - |
| Service Mesh | ✅ | - | - | - | - |
| Immutable Infra | ✅ | - | - | - | - |

### Unique Perspectives by Organization

- **CNCF**: Most comprehensive/canonical. v1.1 adds "sustainable" and "secure" — reflects industry maturity
- **Google**: Most prescriptive with 5 concrete pillars; emphasizes self-healing
- **AWS**: Frames cloud-native as a maturity continuum, not a binary state
- **Oracle**: Pragmatic alignment with CNCF; emphasizes their own CNE tooling
- **IBM**: Philosophical distinction ("how, not where"); clearest cloud-native vs cloud-enabled separation

---

## 3. CNCF 2024 Annual Survey - Key Statistics

- Cloud native adoption: **89%** of surveyed organizations (all-time high)
- Kubernetes usage: **93%** using, piloting, or evaluating
- Service mesh adoption: declining (50% in 2023 → **42%** in 2024)
- AI/ML on Kubernetes: still early, **48%** have not yet deployed AI/ML workloads

- **Ref**: [CNCF Annual Survey 2024](https://www.cncf.io/reports/cncf-annual-survey-2024/)
- **Ref**: [CNCF Research Announcement (April 2025)](https://www.cncf.io/announcements/2025/04/01/cncf-research-reveals-how-cloud-native-technology-is-reshaping-global-business-and-innovation/)

---

## TODO: Next Research Topics

- [ ] AI/ML workloads on cloud-native infrastructure (GPU scheduling, model serving)
- [ ] How AI changes cloud-native practices (AI-assisted operations, AIOps)
- [ ] Cloud-native AI platforms (Kubeflow, Ray, vLLM on K8s)
- [ ] The "AI-native" concept vs "cloud-native" — evolution or new paradigm?
- [ ] Talk structure and slide outline