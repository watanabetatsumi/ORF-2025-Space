# BP-Socket 導入ガイド

## 概要

このシステムは2つのBP（Bundle Protocol）トランスポートをサポートしています：

1. **ion_cli** - 既存のbpsendfile/bprecvfileコマンドを使用（全OS対応）
2. **bp_socket** - AF_BPソケットを直接使用（Linux専用、自動再接続機能付き）

## セットアップ

### 前提条件

- Linux環境（bp-socketモード使用時）
- Go 1.20以上
- bp-socketリポジトリ: https://github.com/DTN-MTP/bp-socket

### 1. bp-socketのインストール（Linuxのみ）

```bash
# リポジトリをクローン
git clone https://github.com/DTN-MTP/bp-socket.git
cd bp-socket

# ビルド
make

# カーネルモジュールをロード
sudo insmod bp_socket/bp.ko

# デーモンを起動
sudo ./daemon/bp_daemon &
```

### 2. 設定ファイルの編集

`cmd/config/config.go`を編集：

#### ION CLIモード（既存）
```go
BPGateway: BpGateway{
    TransportMode: "ion_cli",
    Host:          "localhost",
    Port:          8081,
    Timeout:       30 * time.Second,
}
```

#### BP-Socketモード（Linux専用、推奨）
```go
BPGateway: BpGateway{
    TransportMode: "bp_socket",
    Timeout:       30 * time.Second,
    BpSocket: BpSocketConfig{
        LocalNodeNum:     149,  // 自ノードのIPN番号
        LocalServiceNum:  1,    // 送信用サービス番号
        RemoteNodeNum:    150,  // 相手ノードのIPN番号
        RemoteServiceNum: 1,    // 相手のサービス番号
    },
}
```

### 3. ビルドと実行

```bash
cd backend-server
go build ./cmd/app/
./app
```

## 新機能: 自動再接続

bp-socketモードは**送信・受信の両方で自動再接続**に対応しています。

### 動作の仕組み

- **送信エラー時**: 即座に再接続を試行（最大3回、バックオフ付き）
- **受信エラー時**: 3回連続エラー後に再接続を試行
- **再接続成功**: 通信を継続（アプリケーション再起動不要）
- **再接続失敗**: ログに記録して受信ループを停止

### 利点

1. **デーモンの一時停止に対応**: `bp_daemon`を再起動しても通信が自動復旧
2. **カーネルモジュール再ロードに対応**: モジュールを再ロードしても自動復旧
3. **高可用性**: 一時的な障害でアプリケーションを再起動する必要なし

## システム構成

```
Application
    ↓
BpGateway インターフェース
    ↓
├─ IonCLIGateway (CLIツール経由)
└─ BpSocketGateway (ソケット直接 + 自動再接続)
       ↓
   bpsocket.Connection (再接続ロジック)
       ↓
   bpsocket.Socket (低レベルAF_BP操作)
```

## トラブルシューティング

### "protocol not supported" エラー

**原因**: カーネルモジュールが未ロード

**解決方法**:
```bash
sudo insmod /path/to/bp.ko
lsmod | grep bp  # 確認
```

### "address already in use" エラー

**原因**: 同じEIDが既にバインドされている

**解決方法**:
- 既存プロセスを終了
- または`LocalServiceNum`を変更

### "bp-socket is only supported on Linux" エラー

**原因**: Windows/macOSでbp-socketモードを使用しようとした

**解決方法**:
```go
TransportMode: "ion_cli"  // こちらに変更
```

### "Too many errors, attempting reconnect" ログ

**これは正常な動作です**

3回連続で受信エラーが発生すると、自動的に再接続を試行します。

**確認項目**:
- `bp_daemon`が動作しているか確認: `ps aux | grep bp_daemon`
- カーネルモジュールがロードされているか確認: `lsmod | grep bp`

**再接続が成功すると**:
- ログに `[BpSocket] Reconnect successful, resuming receive` が表示
- 通信が自動的に再開

**再接続が失敗すると**:
- ログに `[BpSocket] Reconnect failed` が表示
- アプリケーションを再起動

### タイムアウトエラー

**原因**: 相手ノードが応答しない

**解決方法**:
```go
Timeout: 60 * time.Second  // タイムアウトを延長
```

### データが切断される

**原因**: バンドルサイズが4MBを超えた

**ログ**: `WARNING: Received X bytes (buffer limit), possible truncation`

**解決方法**:
- データを分割して送信
- または圧縮してから送信

## テスト

### 自動テスト

```bash
# すべてのテストを実行
go test ./...

# ゲートウェイテストのみ
go test ./internal/infrastructure/gateway/ -v

# カバレッジ付き
go test ./internal/infrastructure/gateway/ -cover
```

### 手動テスト

詳細は `TEST_CHECKLIST.md` を参照してください。

主要なテストシナリオ:
1. 基本通信テスト
2. 自動再接続テスト（受信エラー）
3. 自動再接続テスト（送信エラー）
4. グレースフルシャットダウン
5. 同時リクエスト

## 重要な注意事項

### ソケット定数について

`AF_BP = 28`とソケット構造体は**bp-socketカーネルモジュールと完全に一致**する必要があります。

```c
// カーネルモジュールの定義
#define AF_BP 28
struct sockaddr_bp {
    __kernel_sa_family_t sbp_family;  // uint16
    __u64 node_num;                   // uint64
    __u64 service_num;                // uint64
};
```

### 制限事項

- **プラットフォーム**: bp-socketはLinux専用
- **バンドルサイズ**: 最大4MB
- **並行性**: 1つの受信ゴルーチン
- **暗号化**: 未実装（必要に応じてアプリ層で実装）

### 推奨設定

本番環境（高可用性が必要）:
```go
BPGateway: BpGateway{
    TransportMode: "bp_socket",  // 自動再接続
    Timeout:       60 * time.Second,
    BpSocket: BpSocketConfig{
        LocalNodeNum:     149,
        LocalServiceNum:  1,
        RemoteNodeNum:    150,
        RemoteServiceNum: 1,
    },
}
```

開発環境:
```go
BPGateway: BpGateway{
    TransportMode: "ion_cli",  // 簡単にセットアップ可能
    Timeout:       30 * time.Second,
}
```

## 動作確認

### 1. カーネルモジュール確認
```bash
lsmod | grep bp
```

### 2. デーモン確認
```bash
ps aux | grep bp_daemon
```

### 3. アプリケーション起動
```bash
./app

# 期待されるログ出力
# [BpSocket] Gateway started: ipn:149.1 -> ipn:150.1
```

### 4. 再接続テスト
```bash
# 別ターミナルでdaemonを再起動
sudo pkill bp_daemon
sleep 5
sudo /path/to/bp_daemon &

# ログを確認
# [BpSocket] Recv error (1): ...
# [BpSocket] Recv error (2): ...
# [BpSocket] Recv error (3): ...
# [BpSocket] Too many errors, attempting reconnect
# [BpSocket] Reconnect attempt 1/3
# [BpSocket] Reconnected: ipn:149.1
# [BpSocket] Reconnect successful, resuming receive
```

## 参考情報

- bp-socketプロジェクト: https://github.com/DTN-MTP/bp-socket
- ION-DTN: https://sourceforge.net/projects/ion-dtn/
- Bundle Protocol RFC 9171: https://www.rfc-editor.org/rfc/rfc9171.html
- テストチェックリスト: `TEST_CHECKLIST.md`

## サポート

問題が発生した場合:
1. ログを確認（`[BpSocket]`または`[IonCLI]`タグ）
2. カーネルログを確認: `sudo dmesg | grep bp`
3. デーモン状態を確認: `ps aux | grep bp_daemon`
4. 上記のトラブルシューティングを参照
5. 必要に応じて `TEST_CHECKLIST.md` の手動テストを実行
