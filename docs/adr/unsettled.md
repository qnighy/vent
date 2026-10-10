完全に決まりきっていないものはここに記載。

## 表層構文の方針

式ベースであることは ADR-0002 に述べた。

- semicolon vs. newline については newline を許可しつつ、 semicolon 入りでフォーマットする方向 (JavaScript 風) に寄せたい。ただ、 newline が構文を複雑化させるのは事実なので事後的な整理は不可欠。
- キーワード: ここはかなり迷っているが、基本的には TypeScript, Rust, Go, C くらいの順番で参照したい。関数宣言のキーワードについてはいずれかのキーワードを選択するという案のほかに、キーワードの長さによってそれぞれ異なる implicit effect を持つ (たとえば fn は pure total, function は I/O 込みなど) といった案もある。これは effect を実装する過程で判断する。

## 部分型モデル

- 型システムは refinement types を採用する。すなわち公称的に振る舞う N-type に対して、その部分型である R-type が派生し、ユーザーはこの R-type を指示する (N-type はソースコード中に直接出現しない)。R-type は N-type が一致するときに限り lattice operation が可能。値の振る舞いは N-type に依存するが R-type には (原則として) 依存しない。 Refinement は実際のところは型の一部というよりも Hoare precondition / postcondition の sugar であると考え、 flow type 推論ではこのように扱う。ただし、こうした部分型のサポートは明確にしなければいけない仕様も多いため追って追加する。まずは nominal type でよい。

## 整数モデル

- int は多倍長整数。 i64 / u64 は wrapping semantics を持つ固定長整数としたい。ただ、こうした整理は追って行う。実装上の都合で、初期は固定長整数でよい。
- 部分型との整合性は追って考える。 i64 ⊆ int となる可能性もあるが、演算との兼ね合いがポイントか。

## 局所静的エラーの実行モデル

- 局所静的エラーは abort (unrecoverable error) 扱い。

## 値意味論

- Value-by-default。構造上ヒープに出るような場合でも Rust の Box あるいは Rc のように振る舞う。生存解析によって自動的に clone する想定。
- ABI 上 value 渡しになるか immutable reference 渡しになるかについては今のところ完全なルールが決まっていない。大まかな想定は以下。
  - 静的な呼び出しの場合は実装に合わせた呼び出し形式にして最適化する。
  - 動的呼び出しの場合、引数は ref 戻り値は owned value をデフォルトにする。 (キャストをどうするか……)
  - 渡し方を明示するオプション?
    - Rust のように immutable reference を lifetime 共々明示する需要は考えられるので、「入力を x: T と書くと自動的に x: &T として実現される場合があり、所定の構文でオプトアウト」といった整理も考えられる。
- &mut T 相当のものの扱いは原則 Rust と同じ (ライフタイムを持ち ownership を持つが、 reborrowing によって ownership がある程度隠蔽される)。ただ、何らかの sugar で書きやすくすることは考えられる。また、 &mut T からの読み出しの auto-clone 処理については underspecified な部分が残っている。
  - これには two-phase borrow 相当のパターンなど write-read hazard のケースの判断も含まれる。

## Auto-clone 仕様の leak について

- Rust が NLL を仕様としているのと同様、 Vent の auto-clone の振る舞いは仕様の一部である。 CFG への変換および生存解析の手順がそのまま仕様化されるイメージ。
- clone できない場合のエラーの発生条件や clone のコスト管理は処理系依存にしない。

## Leibniz equality

== は Leibniz equality 的である必要がある。つまり、等価ならば後続処理は同じように振る舞わなければならない。必然的に、 == のオーバーロードは容易には提供できない。

特定の組み込み effect handler がこれを迂回する機能を提供することは考えられる。このような迂回路は特定の実行環境 (ユニットテスト環境やコンパイラプラグイン等) では提供されない。

## Reference identity

`Arc<Mutex<T>>` に相当するコンテナ (`Ref<T>` あるいは `Handle<T>` とでも呼ばれるか？) が提供される。これに対する == は参照としての同一性であり、複製は shallow clone である。

Rust と異なり、 shared reference + interior mutablity は不可分に提供される。特定条件下 indirection を飛ばす最適化については必要があれば検討する。この仕組みと、 `Arc<T>` 単体での提供 (= 意味論を変えないため optimization annotation としての提供になる予定) は独立している。

`Ref<()>` は synthetic identity の払出し機能として使える。

## Closure equality

Closure with implicit capture と closure with explicit capture で扱いが異なる。

- closure with implicit capture は比較不可能、あるいは synthetic identity (closure 式ごとに unique) で提供する。
- closure with explicit capture は式ノードの同一性と capture の等価性で比較される。ジェネリクスとの兼ね合いについては要検討。また、式ノードの同一性基準をどこまで hoist するかについては不確定。少なくとも、同じ module source であっても module instance が異なれば別の closure に解決されなければならない。

## Map / Set

Map / Set について、まず大前提は「ハッシュ順序を露出しない」かつ「Leibniz equality のルールを守る」。これを守る実装はいくつかあるが、どの形態を標準提供するかは悩みどころ。

たとえば、 Go 方式 (キー順序をもたない) で提供し、キー列挙の方法として

- pure total total order を使ってソートして取得 (total total = 比較不能も計算不可能もない)
- pure total monoid を使って reduce して取得

を与えるという方法は考えられるが、問題がキー型の提供方式に移ってしまう。

これとは別にキー順序を記憶する assoc array を提供することも考えられる。これは JSON などの扱いでしばしば自然な選択肢である。しかし、 Leibniz equality の関係で、 assoc array の == はユーザーの期待に反する可能性がある。

## 浮動小数点数

浮動小数点数は全ての有限値と正負の無限大、そして単一の NaN からなる集合とみなす。 IEEE 754 に定める比較は別の演算子として提供する。 f64 を含む型の Leibniz 比較は一貫性の観点からむしろ言語レベルで明示的に許可するが、かわりに lint によって怪しいパターンを発見的に報告する。

## クロージャによる書き込みキャプチャの扱い

クロージャが外部環境に書き込む場合はデフォルトで reference capture とする (Rust における move capture 相当を書きやすくする手段は別途検討)。 exclusive mutable reference をとってキャプチャするため、 lifetime の制約が発生する。

## I/O effect の扱い

- effect の扱いについては決まっていないことが多い。初期実装では effect を管理せず一般的な言語のように leak させてOK。
- 戻り型アノテーションによって effect を表現するか、引数が暗黙的に capbility を連れてくるか？
- effect 同士の可換性も表現したくなるかも。その場合 &mut が持つ可換性保証との同一視も考えられる？

## Partial runtime semantics

基本的な方針として、なるべく安定的で well-specified な意味論は与えたい。ただ、これは型システムの複雑性との両立が難しい。

- 大まかには、動的言語のセマンティクスに型検査を入れる (Curry-style) よりも、静的言語のセマンティクスで static typing が整合しない部分に楔を入れて形式的に一貫させる (Church-style + type-guided patch) という形で考えたい。
- 大きな問題として、 runtime semantics を安定させるために型エラーの発生位置をコントロールしなければいけないという問題が挙げられる。一般的な静的型付け言語では注意されない部分なので慎重に考える必要がある。
- 関連する話として、型推論においてHM型の逆行推論 (字句的に後ろの処理によって字句的に手前の処理が決まってしまう) は制限したい。これにより順序のよい型検査規則が出てくるとよいのだが……
  - ただ、ループがある場合は、字句的に後ろであることと実行順序的に後ろであることが必ずしも一致しない点には注意が必要。型推論の結果をもとに実行時意味論が決まるもの (Rust の trait のようなものを想定; Vent では N-type でこれが起こりうる) について完全に時相的な依存順序が保証されるものではなさそう。

## インタプリタの設計

MIR ベースにする。 MIR は auto-clone 実装のタイミングで必要になるので、その時点で対応する。

## ユニットテストのための構文 (専用 vs 汎用)

後回しにする。開発序盤の設計はやりやすいほうでOK。

## 組込み関数呼び出しの設計

初期実装では基本的に設計にはこだわらないが、 platform-dependent な仕組み (すなわち Rust 的な意味での unsafe) は露出しない。これはセルフホスト時に libc 相当部分のブートストラップが必要になってから考える。

ただし、言語の不変条件契約にまつわる unsafe (Haskell 流の unsafe) については必要に応じて導入していく。特に決定論性の保証や Leibniz equality の保証を証明を迂回して実装するにはゲーティングが必要。これについてもユーザーに提供するまでには整理が必要 (証明するための仕組みを提供するなど)。
