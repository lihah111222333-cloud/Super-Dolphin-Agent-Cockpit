# Production bootstrap provisioning

首次 production gate 不能从当前 checkout、候选 CLI 或宿主 `go test` 建立信任。
`super-dolphin-gate provision production` 只安装发布方已经签署的外部信任闭包；它不会生成发布根、不会自签当前源码，也不会预写 `accepted-image.json`。

## 发布方输入

发布方必须通过独立可信渠道交付下列文件或值：

- 一个已签名的 bootstrap root。root 固定 `repo_id`、无凭据 remote、trusted ref、baseline commit/tree、policy/input/toolchain digest、image schema、candidate registry、immutable OCI runner 完整 identity、controller SHA-256、macOS designated requirement、bootstrap Ed25519 public key 和 verifier version。
- bootstrap root signer 的 Ed25519 public key。对应 release private key 不得进入目标机器、仓库、controller 或 runner 容器。
- 与 root 中 bootstrap public key 配对的 0600 bootstrap controller key 文件。该 key 由发布流程安全封装给目标机器；provision 不会用当前 checkout 生成或替换它。
- 经发布流程构建和签名的 `super-dolphin-gate` macOS controller artifact。root 中的 digest 和 designated requirement 必须逐项匹配。ad-hoc codesign 只能用于开发 fixture，不能声明组织身份或 Keychain ACL。
- root 固定的 OCI bootstrap runner。目标平台 manifest、config 和 rootfs diff IDs 必须都可由 Docker content store 观察；tag 或单个 digest 不足以通过验证。
- 独立的 receipt 与 ActionGrant Ed25519 authority bundles，以及生产 seccomp profile。ActionGrant signer 必须与 receipt/bootstrap signer 分离。

runner image 必须包含 Docker Buildx、Git 和同一发布 gate artifact，后者安装为 `/usr/local/bin/super-dolphin-bootstrap`。runner 仅获得非秘密 request 与 Docker socket，不会获得 release/bootstrap/receipt/ActionGrant 私钥。

## 本机准备

在仓库和任何 worktree 之外创建私有目录。示例中的具体路径可替换，但 manifest、所有 key/root/profile 文件和 install parent 必须是 canonical path；私有目录为 0700，输入文件为 0600。

```sh
install -d -m 700 "$HOME/.local/share/super-dolphin-gate-provision"
install -d -m 700 "$HOME/.local/share/super-dolphin-gate-production"
install -d -m 755 "$HOME/.local/bin"
chmod 600 "$HOME/.local/share/super-dolphin-gate-provision/"*.json
```

manifest 本身也是仓库外 0600 文件：

```json
{
  "schema_version": 1,
  "install_root": "/Users/NAME/.local/share/super-dolphin-gate-production/root-v1",
  "launcher_path": "/Users/NAME/.local/bin/super-dolphin-gate",
  "controller_binary": "/Users/NAME/.local/share/super-dolphin-gate-provision/super-dolphin-gate-controller",
  "bootstrap_root_file": "/Users/NAME/.local/share/super-dolphin-gate-provision/bootstrap-root.json",
  "bootstrap_controller_key_file": "/Users/NAME/.local/share/super-dolphin-gate-provision/bootstrap-controller-key.json",
  "receipt_key_file": "/Users/NAME/.local/share/super-dolphin-gate-provision/receipt-key.json",
  "action_grant_key_file": "/Users/NAME/.local/share/super-dolphin-gate-provision/action-grant-key.json",
  "seccomp_profile": "/Users/NAME/.local/share/super-dolphin-gate-provision/seccomp.json",
  "trusted_source_root": "/Users/NAME/Library/Caches/super-dolphin/localci",
  "platform": "linux/arm64",
  "trusted_root_keys": [
    {
      "signer": {"key_id": "RELEASE_KEY_ID", "key_epoch": 1, "algorithm": "ed25519"},
      "public_key": "RELEASE_ED25519_PUBLIC_KEY_BASE64"
    }
  ],
  "candidate_ttl_seconds": 3600,
  "promotion_poll_millis": 100,
  "action_grant_ttl_seconds": 60
}
```

authority bundle 使用严格 JSON：

```json
{
  "signer": {"key_id": "AUTHORITY_KEY_ID", "key_epoch": 1, "algorithm": "ed25519"},
  "public_key": "ED25519_PUBLIC_KEY_BASE64",
  "private_key": "ED25519_PRIVATE_KEY_BASE64"
}
```

执行唯一安装命令：

```sh
/path/from/trusted-release/super-dolphin-gate \
  provision production \
  --manifest "$HOME/.local/share/super-dolphin-gate-provision/provision.json"
```

命令在发布 install root 前完成 root 验签、controller digest/codesign、bootstrap key 配对、runner immutable identity 和 bare trusted repository baseline commit/tree 验证。它随后原子发布 0700 install root，并原子安装 launcher；launcher 固定注入生成的 `SUPER_DOLPHIN_GATE_PRODUCTION_CONFIG`。不要再在 hook 中手工设置该变量。

`trusted_source_root` 必须预先创建为 0700 canonical directory，并包含平台解析出的 coordinator runtime root；macOS 默认值就是 `~/Library/Caches/super-dolphin/localci`。

## 首次 submit

安装完成时 accepted store 必须为空。第一次 hook/submit 在仓库外锁下执行一次 bootstrap controller：controller 从 root 固定的 runner manifest 创建 4 CPU、8 GiB、read-only、cap-drop-all、no-new-privileges 的一次性容器；runner 从 root 固定 baseline 构建并推送 candidate。宿主 verifier 复核 request、argv、labels、env、资源、Docker socket bind、inspect/log digest、candidate identity、generation-one record 和两层 Ed25519 签名，才原子 bootstrap `generation=1`。同一次 submit 随后从新 accepted immutable image 启动 fresh gate 容器。

并发首次 submit 共享同一仓库外锁；只有赢家执行 controller，其他调用读取同一 generation 1。任何 root/key/digest/codesign/runner/container/record 漂移都失败，且不会回退到宿主 Go、npm、make 或 candidate CLI。

## 外部前置与残余边界

- Docker Desktop、目标 runner manifest、baseline remote 和 candidate registry 必须在执行前可达。runner 不接收私钥或宿主 credential 文件，因此 baseline remote 必须允许无交互只读访问，candidate registry 必须允许该外部 builder 身份推送；凭据型 registry 需要发布方提供 Docker/BuildKit 外部身份方案，当前 provision 不会伪造一个。
- macOS designated requirement 证明执行 artifact 的 Code Signing 身份，不证明 Keychain ACL。若组织策略要求 Keychain ACL，发布方 installer 必须在调用 provision 之前独立完成并证明该 attestation；本命令 fail-fast 消费文件材料，不生成 ACL evidence。
- install root 发布后不允许覆盖。轮换 release root、controller、runner 或 authority 时使用新的 install root 和经发布方签名的新 manifest/root，再原子切换 launcher。
