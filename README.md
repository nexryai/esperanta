# Esperanta

Goで実装した、YAML設定による接続元制限付きのHTTP forward proxyです。
HTTP転送とHTTPSのCONNECTトンネルに対応します。TLSはクライアントと
接続先の間で終端し、プロキシは復号しません。

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
未知のキー、重複キー、不正なIPやポート、複数YAML文書は起動時に拒否します。
設定変更は再起動で反映します。

接続先のDNS解決結果を検査し、検査済みのIPへ直接接続します。
非公開IPが混在するDNS応答も拒否します。内部サービスへの接続が必要な場合は
`deny_private_destinations: false`に変更できます。

## Snapパッケージ

Snapcraftが利用でき、strict confinementを完全にサポートするLinux環境で
ビルド・実行します。CIはUbuntu 24.04でパッケージをビルドし、インストール、
実行UID、capabilities、再起動、停止を検証します。

```sh
snapcraft
sudo snap install --dangerous ./esperanta_0.1.0_amd64.snap
sudo snap services esperanta
sudo snap logs esperanta.esperanta
```

`--dangerous`はローカルの未署名パッケージをインストールするためのオプションです。
confinementは`strict`のままです。

設定ファイルは`/var/snap/esperanta/current/config/config.yaml`です。
インストール時に既定設定を配置します。設定はリビジョンごとの`SNAP_DATA`に
置くため、更新時に引き継がれ、Snapのロールバック対象になります。

```sh
sudo snap stop esperanta.esperanta
sudoedit /var/snap/esperanta/current/config/config.yaml
sudo chmod 0400 /var/snap/esperanta/current/config/config.yaml
sudo chown 584792:0 /var/snap/esperanta/current/config/config.yaml
sudo snap run esperanta --check
sudo snap start esperanta.esperanta
```

Snapサービスのランチャーはrootで起動されますが、`setpriv`で
`_daemon_`（UID/GID 584792）へ切り替え、補助グループとcapabilitiesを除去してから
Goプロセスを起動します。`no-new-privs`も設定します。
Go側でも実UIDまたは実効UIDがrootなら起動を拒否します。
待ち受けポートは1024以上に限定します。

`system-usernames`による権限降格は[Snapの公式方式](https://snapcraft.io/docs/explanation/snap-development/system-usernames/)
に従います。接続するインターフェースは`network`と`network-bind`だけです。
AppArmor・seccomp・名前空間による[strict confinement](https://snapcraft.io/docs/explanation/security/snap-confinement/)
を使用し、ホストのホームディレクトリやシステムファイルへのアクセス権は追加しません。
ホストで`snap debug confinement`が`strict`を返すことを確認してください。

## 性能と検証

接続先へのkeep-alive接続を再利用し、HTTP/2対応の接続先にも対応します。
レスポンスを全体としてメモリーへ読み込まず、32 KiBの再利用バッファーで転送します。
CONNECTもストリームとして双方向に転送し、終了時には両側の接続を閉じます。
処理数上限ではHTTP 503を返し、TCP接続数上限では追加接続を閉じます。

```sh
go test -race ./...
go vet ./...
go test ./internal/proxy -run '^$' -bench BenchmarkForward -benchmem
```

テストは接続元・ポート制限、ヘッダー除去、接続再利用、実際のHTTPS通信、
CONNECTに続けて送ったデータ、停止時のトンネル破棄、接続数上限を検証します。
ベンチマークはloopback上の32 KiB HTTPレスポンスを並列転送します。
実ネットワークでの性能はCPU、帯域、接続先、同時接続数に依存します。

SIGTERM/SIGINTで待ち受けを停止し、CONNECTトンネルを閉じます。
通常のHTTP処理は`shutdown_timeout`まで終了を待ちます。
HTTP UpgradeはCONNECT経由で使用してください。接続元の認証はIP制限です。
