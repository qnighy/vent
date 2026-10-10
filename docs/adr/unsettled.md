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

## I/O effect の扱い

- effect の扱いについては決まっていないことが多い。初期実装では effect を管理せず一般的な言語のように leak させてOK。
- 戻り型アノテーションによって effect を表現するか、引数が暗黙的に capbility を連れてくるか？
- effect 同士の可換性も表現したくなるかも。その場合 &mut が持つ可換性保証との同一視も考えられる？
