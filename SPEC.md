# lapkbd 仕様

外付けキーボードを繋いでいる間だけ、ノート PC の内蔵キーボードを無効にする常駐ツール。
外付けキーボードを内蔵キーボードの上に置いて使う人のためのもの。

## 対象

- KDE Plasma 6 の Wayland セッション (KWin の D-Bus API を使う)
- root 権限は不要

## やらないこと

- 他のデスクトップ環境への対応
- トラックパッドの設定 (タップ、スクロールなど)。これは KDE の設定や dotfiles の役割
- ログイン画面 (SDDM) での無効化。KWin のセッションの外なので効かない
- 外付けキーボードの自動判定。仮想キーボードを外付けと誤認すると内蔵キーボードが止まるので、
  名前を明示する

## 設定

`$XDG_CONFIG_HOME/lapkbd/config.toml` (既定は `~/.config/lapkbd/config.toml`)

```toml
internal = "Apple Inc. Apple Internal Keyboard / Trackpad"
external = ["HHKB-Hybrid_1 Keyboard", "PFU Limited HHKB-Hybrid"]
```

- `internal`: 内蔵キーボードの KWin 上の名前。必須
- `external`: 外付けキーボードの KWin 上の名前の一覧。1 つ以上必須。
  同じキーボードでも USB と Bluetooth で名前が違うことがあるので、使う分だけ並べる
- 名前は完全一致で比べる
- 未知のキー、空の値、`internal` と同じ名前が `external` にある設定はエラーにする

## コマンド

| コマンド | 役割 |
|---|---|
| `lapkbd init` | 今見えているキーボードから選んで設定を作る。`-internal` / `-external` を渡すと対話なし。既存の設定は `-force` が無ければ上書きしない |
| `lapkbd status` | 設定、サービス、外付けの接続、内蔵の状態、KWin が見ているキーボードを表示する。設定が無くても一覧は出す |
| `lapkbd run` | 常駐する。systemd から使う |
| `lapkbd restore` | 内蔵キーボードを有効に戻す。困ったときと、サービス停止時の後始末に使う |
| `lapkbd install-service` | systemd のユーザーユニットを書く。有効化はしない |

`run` と `status` と `restore` と `init` は `-config <path>` で設定ファイルを指定できる。

## 動作の決まり

### 判定

- **外付けが接続中**: `external` の名前で、英数字キーボードで、KWin で有効なデバイスが 1 つ以上ある
- **内蔵キーボード**: `internal` の名前で、英数字キーボードのデバイス
- **同名のトラックパッド**: `internal` の名前で、タッチパッドのデバイス

MacBook では内蔵キーボードとトラックパッドが同じ名前で同じ設定グループを共有する。

### 状態を合わせるとき

- 起動時に 1 回
- KWin の `deviceAdded` / `deviceRemoved` を受けて、信号が 1 秒途切れたとき 1 回
  (スリープ復帰で信号がまとめて来ても 1 回にまとめる)
- KWin が起動し直したとき (`NameOwnerChanged`)

### 合わせ方

1. 外付けが接続中なら内蔵キーボードを無効に、そうでなければ有効にする
2. 同名のトラックパッドは常に有効にする
3. `enabled` は **今の値と違うときだけ** 書く。トラックパッドは書くとタッチが切れる
4. `kcminputrc` の内蔵キーボードの `Enabled` を `true` に書き直す。KWin は無効にした値を
   ここへ保存するので、残すと次の KWin が内蔵キーボードとトラックパッドを無効で始める
5. `Enabled` 以外のキーには触らない
6. KWin から読み直した実際の値をログに出す

### 安全のための決まり

キーボードが 1 つも使えない状態を避けることを最優先にする。

- 外付けの状態が読めないときは「接続していない」として扱う (内蔵は有効の側に倒す)
- `SIGTERM` / `SIGINT` で止まるときは内蔵キーボードを有効に戻す
- `SIGKILL` などで落ちたときは、ユニットの `ExecStopPost` の `lapkbd restore` で戻す
- サービスが動かなくても、次のログインでは内蔵キーボードとトラックパッドは有効で始まる
- KWin が見つからなくても終了せず、起動を待つ。セッションバスが切れたら異常終了して
  systemd に再起動を任せる

## コードの構成

| パッケージ | 役割 |
|---|---|
| `cmd/lapkbd` | `cli.Run` を呼んで、その戻り値で終わるだけ |
| `internal/cli` | サブコマンド、出力、終了コード (0: 成功、1: 失敗、2: 使い方の誤り) |
| `internal/guard` | 内蔵キーボードをいつ有効にするかの判断と適用。KWin は `Backend` で差し替える |
| `internal/kwin` | KWin の D-Bus API と `kwriteconfig6` |
| `internal/config` | 設定ファイルの読み書きと検証 |

## 受け入れ条件

dotfiles の `hhkb-internal-kbd` で確認した項目を最低条件とする。

| ID | 条件 | 確認方法 |
|---|---|---|
| A1 | 再ログイン後もトラックパッドの設定 (右クリックなど) が保たれる | `kcminputrc` と KWin の値の比較、実機 |
| A2 | 外付けを切ると内蔵が有効に、繋ぎ直すと無効になる | ログと KWin の値、実機 |
| A3 | スリープ復帰後もトラックパッドのタッチが切れない | トラックパッドの `enabled` への書き込みの監視、実機 |
| A4 | 動作中も `kcminputrc` の `Enabled` が `true` のまま | ファイルの確認 |
| A5 | 止めると内蔵が有効に戻る (`SIGTERM` と `SIGKILL`) | KWin の値。`SIGKILL` は本体の PID に `kill -9` を送る。`systemctl kill` は `ExecStopPost` まで止めるので確認に使えない |
