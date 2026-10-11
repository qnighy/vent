# vent

プログラミング言語になる予定の場所

## 基本コンセプト

これらを全て実現できるかはわからない。途中で方針が変わる可能性はある。また、このリストは仕様ではなく、おしなべて underspecified である。詳細は実装しながら詰めていく。

- 意味論 - 値
  - 現代的な手続き型言語。分離論理という革命を実用のグラウンドに落とし込んだ Rust の軌跡を追い、手続き的でありながら正しい契約を行える抽象を提供する。特に、データ構造は value-by-default (インライン, Box, または Rc 相当) 意味論で扱われ、共有書き換え参照は明示しないとできない
  - 一方でシステムプログラミング言語に特化した Rust とは方向性を異にし、抽象的な意味論にとって不要な具象 (Box のような) はなるべく捨象する。
- 意味論 - 副作用
  - 決定論性とエフェクトシステム。エフェクトで明示しない限りは外界にアクセスできず、非決定論的な振る舞いもできない。これによりユニットテストが完全に決定論的になる。
- 意味論 - 静的検査
  - コンパイルを通す面倒さ(起動時コストを含む)からの解放。実行前の静的検査を局所化し、実行がプログラムツリー全体の静的検査に依存しないようにする。静的検査が通っていない場所にも partial runtime semantics を定義する (基本的には runtime error にマッピング)。もちろん明示的に型検査を要求すれば全体が検査される。
- 統語論
  - 構文は原則として広義の C family に寄せる。必要な合理化は行いつつ、任意性のある部分は見慣れた構文に近付ける。
- 互換性
  - 形式的な互換性 (lib v1.1 でコンパイルできれば lib v1.2 でもコンパイルできる) を念入りに保証する。特に、定義の追加が利用側を壊さないような仕組みと、ライブラリ側の互換関係を検査する仕組みを作る
  - accidentally compiles を避ける。たとえば、 diamond dependency が偶然 converge している場合に両経路を同一視しないなど。
- 閉じた世界
  - Go のようにコンパイラエコシステム、ネイティブ実装、 libc 相当のグルーを全て自前で持つことを目指す
  - FFI の実装は意図的に遅らせる。かわりに、 wasm による決定論的な pseudo-FFI の実装はやや優先する
- エコシステム
  - Deno がかつて目指したような、 batteries included かつ scale from script to full project な環境作り。
  - サードパーティーパッケージを受け入れつつも、なるべくメンテナンス責任を集約できるようなパッケージエコシステム。 fat std と thin std の折衷、つまり言語コアと結合した std は最小限にしつつ、別立てでバージョン管理され batteries included で提供される公式パッケージ群としての std は十分に広くとる。また、 adopt を積極的に行う仕組みを作る。加えて、開発者とメンテナの分離を進め、 contributed by everyone, published by selected persons な周縁パッケージを充実させる
- コンセプト
  - 全体を通して、正しいプログラムを楽しく書けることを目標にしている。

## 開発言語

- ブートストラップ言語
  - Go によるインタプリタ
- ドキュメンテーション、コメント、issue, PR
  - 日本語・英語の混在 (作者が日本語話者のため)
  - 将来的に英語への統一は考える
  - AI Agent は英語を優先的に使うこと

## 開発方針

- Go のインタプリタを作る → 言語仕様を調整しながら発展させる → コンパイラを書いてセルフホスト (ターゲットは wasm32, x86-64, aarch64)
- 適宜 ADR は残す。 ADR は重要な設計上の選択を記録するために使い、実装詳細は説明しない。 ADR の清書は面倒でも人間が行う。 AI Agents はどのような ADR を残すべきかのアドバイスを積極的に行う。
- インタプリタ実装時は、仕様を詰めるよりも大方針を固めることを目標にする。プロトタイプを先行させ、軌道修正が必要な箇所について人間が指示する。この時点では、全てのコンセプトが実装されている必要はない。 (たとえばエフェクトシステムの整理は後ろ倒しになる可能性が高い)
- セルフホスト時には仕様を固めながら進める。仕様が決まった部分から実装していく。
- コミュニティー貢献は現時点では想定しない

## Development

- `go run ./cmd/vent run <file>.vent` — run a program. Static errors abort only when the code containing them is executed
- `go run ./cmd/vent check <file>...` — report all static errors
- `go run ./cmd/vent test [-v] <file|dir>...` — run Vent unit tests: parameterless functions named `test_*`, using the builtins `assert`, `assert_eq` and `panic` (a stopgap until test syntax is designed)
- `go test ./...` — run all tests
- Golden tests: `internal/interp/testdata/run/*.vent` (programs), `internal/interp/testdata/test/*.vent` (`vent test` reports) and `internal/check/testdata/*.vent` (`vent check` reports) + expected `*.out`. Regenerate with `go test ./internal/... -run TestGolden -update` and review the diff.
