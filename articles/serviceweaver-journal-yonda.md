---
title: "【論文紹介】マイクロサービスの課題をそれを解説するためのコンセプトを探る"
emoji: "👏"
type: "tech" # tech: 技術記事 / idea: アイデア
topics: []
published: false
---

## はじめに
Googleのエンジニアが2023年6月に発表した[Towards Modern Development of Cloud Applications](https://dl.acm.org/doi/10.1145/3593856.3595909)という論文を紹介する記事です。（PDFへの直リンクは[こちら](https://dl.acm.org/doi/pdf/10.1145/3593856.3595909)）マイクロサービスの課題を分析しそれを解決するためのコンセプトに関して提案した記事であり、またプロトタイプ実装として[ServiceWeaver](https://serviceweaver.dev/)というプロダクトを開発しています。

https://dl.acm.org/doi/10.1145/3593856.3595909

本記事では論文の内容をまとめることで理解を深めながら、さらにはこのコンセプトが解決できなかった課題とどう立ち向かうべきなのか？について考察することを目的としています。

ServiceWeaverについて理解することで論文の内容が分かりやすくなるので、ServiceWeaverについても軽く触れながら解説していきます。

:::message
[今回紹介する論文](https://dl.acm.org/doi/10.1145/3593856.3595909)と[ServiceWeaver](https://serviceweaver.dev/)の内容を知ってもらうのもこの記事の目的です。
日本での注目度が低いと感じているものの、マイクロサービスの課題として挙げられているものは共感でき、提案コンセプトも面白いものと感じています。
:::

## マイクロサービスアーキテクチャの課題とその原因

### マイクロサービスのメリット
さまざまな調査により、ほとんどの開発者が次のいずれかの理由でアプリケーションを複数のバイナリに分割している（マイクロサービスを採用している）ことがわかりました。
1. **パフォーマンス**：個別のサービスを個別にスケーリングできるため、必要な分だけリソースを使用することができます。
2. **耐障害性**：1つのマイクロサービスがクラッシュしても他のマイクロサービスはダウンしないため、バグや障害の影響範囲を制限することができます。
3. **アプリケーション境界の明確化**：APIによってマイクロサービス間を明確に区切ることで、サービス内のコードの複雑化を防ぎます。
4. **柔軟なデプロイ**：異なるバイナリを異なるレートでリリースできるため、より機敏なコードのアップグレードが可能になります。

### マイクロサービスのデメリット
ただし、マイクロサービスアーキテクチャにはデメリットが存在し、これらのデメリットによってメリットが打ち消されてしまう場合もあると分析しています。

1. **パフォーマンス懸念**：データのシリアライズとネットワーク通信はパフォーマンスを下げる。
2. **正確性の懸念**：バージョン間の通信の正確性を担保することが難しい。
3. **サービス全体の管理が大変**：N個のバイナリを管理することが難しい。E2Eテストの難易度が高くなっていく。ローカル環境でアプリケーションを1つ動かしたいだけなのに、複数のリポジトリからコードをプルして動かす必要があったりとローカル環境での開発も大変となる。
4. **APIの変更難易度をあげる**：APIの変更が難しく、互換性を保つため昔のAPIを残しつつ新しいAPIを作成し、すべてのサービスのバージョンアップが終わったら古いAPIを削除するようなデプロイ方法が必要になることも。
5. **アプリケーションの開発を遅くする**：複数のサービスに影響がある変更を加えようとした際に、どうやってN個のマイクロサービスに変更を反映させデプロイするか検討する必要がある。

これらは基本的に、マイクロサービスが論理境界 (コードの記述方法) と物理境界 (コードのデプロイ方法) を混同しているためと筆者は考察しています。
:::message
やや抽象的な考察になっています。私の理解ですが、**開発時にコード上で分割したい関心事の違い（例えば認証認可・顧客管理・商品管理）** に応じたアプリケーションの分割は、必ずしも**実行時の結合度とは一致せず、密に通信するサービス感が地理的に離れたところに配置される** ことによってレイテンシが増加する、といったことが挙げられるのかなと思います。
:::

### マイクロサービスをより良く使うための方法

これらの課題に対する過去の取り組みとして、CICDやgRPC等があげられるものの、デメリット1-5すべてを解消するプロダクトはありません。

筆者らはこの課題5つ全てを解決する手法を提案します。筆者らの手法は以下3つのコンセプトによって構成されます。
1. **開発時には論理モジュール化されたモノリシックアプリケーションであること。**
2. **実行時に論理モジュールに自動かつ動的に物理プロセスを割り当てること。**
3. **バージョンが異なるアプリケーション間で通信しない"アトミックなデプロイ"をすること。**

<!-- | コンセプト | できること | 解決する課題 | -->
<!-- | ---- | ---- | ---- | -->
<!-- | 開発時にモノリス | Text | サービス全体の管理、アプリ開発、API変更難易度 | -->
<!-- | 実行時に動的にプロセス割当て | Text | パフォーマンス | -->
<!-- | アトミックなデプロイ | Text | 正確性、API変更難易度 | -->

## ServiceWeaverとは？
提案コンセプトのイメージが付きやすくなると思うので細かい内容に入っていく前に、簡単にプロトタイプ実装であるServiceWeaverを紹介します。

- Googleのエンジニアが考えたマイクロサービスをよりよく開発・運用するためのフレームワーク
- 開発環境ではシングルバイナリでアプリケーションをモノリシックに開発し、デプロイ時にはマイクロサービスアプリバージョンとしてデプロイすることが出来る
- Goで書かれており、執筆時点(2024年3月3日)では0.23.0が最新でありまだ安定版はない。

### ServiceWeaverの特徴1: 開発時には論理モジュール化されたモノリシックアプリケーションである

### ServiceWeaverの特徴2: 実行時に論理モジュールに自動かつ動的に物理プロセスを割り当てる

### ServiceWeaverの特徴3: バージョンが異なるアプリケーション間で通信しない"アトミックなデプロイ"ができる



## アブストラクト
- アプリケーションをサービスに分割するマイクロサービスアーキテクチャにはデメリットが存在します。これらは基本的に、マイクロサービスが論理境界 (コードの記述方法) と物理境界 (コードのデプロイ方法) を混同しているためです。
- 私の理解ですが、開発時にコード上で分割したい関心事の違い（例えば認証認可・顧客管理・商品管理）に応じたアプリケーションの分割は、必ずしも実行時の結合度とは一致せず、密に通信するサービス感が地理的に離れたところに配置されることによってレイテンシが増加する、といったことが挙げられるのかなと思います。



- この論文では、これらの課題を解決するために、この2つを分離するプログラミング手法を提案します。
- このアプローチでは、開発者はアプリケーションを論理モノリスとして記述し、アプリケーションをデプロイ・実行する方法をランタイムに委ね、アプリケーションをアトミックにデプロイします。
- プロトタイプ実装(=ServiceWeaver)では、アプリケーションのレイテンシを15倍、コストを9倍改善しています。

- アプリケーションをサービスに分割するマイクロサービスアーキテクチャにはデメリットが存在し、アーキテクチャが達成しようとしているメリットを打ち消してしまうことがあります。
- これらは基本的に、マイクロサービスが論理境界 (コードの記述方法) と物理境界 (コードのデプロイ方法) を混同しているためです。
- この論文では、これらの課題を解決するために、この2つを分離するプログラミング手法を提案します。
- このアプローチでは、開発者はアプリケーションを論理モノリスとして記述し、アプリケーションをデプロイ・実行する方法をランタイムに委ね、アプリケーションをアトミックにデプロイします。
- プロトタイプ実装(=ServiceWeaver)では、アプリケーションのレイテンシを15倍、コストを9倍改善しています。

## イントロ

さまざまなインフラストラクチャ チームの内部調査により、ほとんどの開発者が次のいずれかの理由でアプリケーションを複数のバイナリに分割していることがわかりました。
(1) パフォーマンスが向上します。 個別のバイナリを個別にスケーリングできるため、リソースの使用率が向上します。
(2) 耐障害性が向上します。 1 つのマイクロサービスがクラッシュしても他のマイクロサービスはダウンしないため、バグの爆発範囲が制限されます。
(3) 抽象化境界を改善します。 マイクロサービスには明確で明示的な API が必要であり、コードのもつれの可能性は大幅に最小限に抑えられます。 
(4) 柔軟な展開が可能です。 異なるバイナリを異なるレートでリリースできるため、より機敏なコードのアップグレードが可能になります。

