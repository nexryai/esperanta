# Esperanta

Goで実装した、シンプルなHTTP forward proxyです。

## ローカル実行

Goのバージョンは`go.mod`を参照してください。一般ユーザーで実行します。

```sh
go build -trimpath -o bin/esperanta ./cmd/esperanta
./bin/esperanta --config config.yaml --check
./bin/esperanta --config config.yaml
```

別の端末から動作を確認できます。

```sh
curl --noproxy '' --proxy http://127.0.0.1:8080 http://example.com/
curl --noproxy '' --proxy http://127.0.0.1:8080 https://example.com/
```

## YAML設定

全項目を含む設定例は[`config.yaml`](config.yaml)にあります。
初期設定は`127.0.0.1:8080`で待ち受け、loopbackからの接続だけを許可します。
LANに公開する場合は、例えば以下のように変更します。

```yaml
listen: "0.0.0.0:8080"
allowed_sources:
  - "192.168.1.0/24"
  - "192.168.2.10"
allowed_ports: [80, 443]
deny_private_destinations: true
```

`allowed_sources`はIPv4・IPv6の単一IPまたはCIDRです。空のリストは
起動エラーになり、許可されていない接続はTCP接続の時点で閉じます。
判定にはTCP接続元を使用し、`X-Forwarded-For`等は参照しません。
ロードバランサー等を挟む場合、判定対象はその接続元IPになります。

| 項目 | 用途 |
| --- | --- |
| `allowed_ports` | HTTP転送とCONNECTの接続先ポート制限 |
| `deny_private_destinations` | 内部IP、loopback、link-local、共有アドレス等への接続を拒否 |
| `max_concurrent` | 同時処理するHTTPリクエストとCONNECTトンネルの合計上限 |
| `max_connections` | アイドル接続を含むクライアントTCP接続の上限 |
| `max_idle_connections` | プールに保持する接続先へのアイドル接続数 |
| `max_idle_connections_per_host` | 接続先ごとのアイドル接続数 |
| `dial_timeout` | DNS解決を含む接続確立の制限時間 |
| `header_timeout` | クライアント・接続先のHTTPヘッダー待機時間 |
| `request_timeout` | 通常のHTTPリクエスト全体の制限時間 |
| `idle_timeout` | クライアント・接続先のアイドル接続の保持時間 |
| `tunnel_timeout` | CONNECTトンネルの最大寿命 |
| `shutdown_timeout` | 通常のHTTPリクエストの終了待機時間 |

時間は`10s`や`2m`で指定します。省略した項目には既定値を適用します。
設定変更は再起動で反映します。

接続先のDNS解決結果を検査し、検査済みのIPへ直接接続します。
非公開IPが混在するDNS応答も拒否します。内部サービスへの接続が必要な場合は
`deny_private_destinations: false`に変更できます。
