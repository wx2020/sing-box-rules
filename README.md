# sing-box-rules

[![Release](https://github.com/wx2020/sing-box-rules/actions/workflows/release.yaml/badge.svg)](https://github.com/wx2020/sing-box-rules/actions/workflows/release.yaml)
[![GitHub Release](https://img.shields.io/github/v/release/wx2020/sing-box-rules?color=blue)](https://github.com/wx2020/sing-box-rules/releases)
[![License](https://img.shields.io/github/license/wx2020/sing-box-rules)](LICENSE)

自动同步上游 [Loyalsoldier/v2ray-rules-dat](https://github.com/Loyalsoldier/v2ray-rules-dat) 与 [Loyalsoldier/geoip](https://github.com/Loyalsoldier/geoip) 规则发布，并深度优化编译为 **sing-box 专用的二进制规则集 (`.srs`)**。

---

## 🌟 项目特色

- 🔄 **自动同步上游**：通过 GitHub Actions 每日自动检测上游 Release，有更新时自动构建并发布对应版本。
- ⚡ **深度规则精简与优化**：
  - **域名层级去重**：自动识别父级域名后缀（`domain_suffix`），彻底剔除已被父域包含的冗余子域名和完全匹配项，规则体积减少 **50%+**。
  - **规范化格式**：遵循 sing-box 标准规范，标准 `domain_suffix` 无需多余的前缀点（`.`），原生同时匹配根域与所有子域。
  - **IP CIDR 聚合合并**：对 IP 网段进行合并去重与规范排序，消除重叠与碎片网段。
  - **低内存与高性能**：优化后的二进制 `.srs` 格式显著降低 sing-box 客户端内存占用并加快路由匹配。
- 📦 **全套分发渠道**：
  - **GitHub Releases**：提供热门常用 `.srs` 单文件直链，以及全量 `.zip` 打包（`geosite-srs.zip`, `geoip-srs.zip`, `rules-srs.zip`）。
  - **Git 独立分支**：在 `rule-set-geosite` 与 `rule-set-geoip` 分支中保留全量 1900+ 规则的 `.srs` 二进制与 `.json` 源码。
  - **jsDelivr CDN 加速**：支持国内网络环境直接高速拉取。

---

## 🚀 在 sing-box 中使用

在 sing-box 配置文件（如 `config.json`）的 `route` 模块中配置远程规则集 `rule_set`：

```json
{
  "route": {
    "rule_set": [
      {
        "tag": "geosite-category-ads-all",
        "type": "remote",
        "format": "binary",
        "url": "https://raw.githubusercontent.com/wx2020/sing-box-rules/rule-set-geosite/geosite-category-ads-all.srs",
        "download_detour": "direct",
        "update_interval": "1d"
      },
      {
        "tag": "geosite-cn",
        "type": "remote",
        "format": "binary",
        "url": "https://raw.githubusercontent.com/wx2020/sing-box-rules/rule-set-geosite/geosite-cn.srs",
        "download_detour": "direct",
        "update_interval": "1d"
      },
      {
        "tag": "geosite-geolocation-!cn",
        "type": "remote",
        "format": "binary",
        "url": "https://raw.githubusercontent.com/wx2020/sing-box-rules/rule-set-geosite/geosite-geolocation-!cn.srs",
        "download_detour": "proxy",
        "update_interval": "1d"
      },
      {
        "tag": "geoip-cn",
        "type": "remote",
        "format": "binary",
        "url": "https://raw.githubusercontent.com/wx2020/sing-box-rules/rule-set-geoip/geoip-cn.srs",
        "download_detour": "direct",
        "update_interval": "1d"
      }
    ],
    "rules": [
      {
        "rule_set": "geosite-category-ads-all",
        "outbound": "block"
      },
      {
        "rule_set": "geosite-cn",
        "outbound": "direct"
      },
      {
        "rule_set": "geoip-cn",
        "outbound": "direct"
      },
      {
        "rule_set": "geosite-geolocation-!cn",
        "outbound": "proxy"
      }
    ],
    "final": "proxy"
  }
}
```

---

## 🔗 常用规则集地址

### 1. GitHub Releases 渠道 (推荐稳定使用)
直接下载或引用每次自动发布的 Release 资产：

| 规则名称 | 规则说明 | Release 下载直链 |
| :--- | :--- | :--- |
| `geosite-cn.srs` | 国内常见域名 | `https://github.com/wx2020/sing-box-rules/releases/latest/download/geosite-cn.srs` |
| `geoip-cn.srs` | 国内 IP 网段 | `https://github.com/wx2020/sing-box-rules/releases/latest/download/geoip-cn.srs` |
| `geosite-geolocation-!cn.srs` | 非大陆常见域名 | `https://github.com/wx2020/sing-box-rules/releases/latest/download/geosite-geolocation-!cn.srs` |
| `geosite-category-ads-all.srs` | 常见广告拦截 | `https://github.com/wx2020/sing-box-rules/releases/latest/download/geosite-category-ads-all.srs` |
| `geosite-gfw.srs` | GFW 域名列表 | `https://github.com/wx2020/sing-box-rules/releases/latest/download/geosite-gfw.srs` |
| `geosite-apple.srs` | Apple 完整域名 | `https://github.com/wx2020/sing-box-rules/releases/latest/download/geosite-apple.srs` |
| `geosite-apple-cn.srs` | Apple 中国域名 | `https://github.com/wx2020/sing-box-rules/releases/latest/download/geosite-apple-cn.srs` |
| `geosite-google.srs` | Google 域名 | `https://github.com/wx2020/sing-box-rules/releases/latest/download/geosite-google.srs` |
| `geosite-openai.srs` | OpenAI / ChatGPT | `https://github.com/wx2020/sing-box-rules/releases/latest/download/geosite-openai.srs` |
| `geosite-telegram.srs` | Telegram 域名 | `https://github.com/wx2020/sing-box-rules/releases/latest/download/geosite-telegram.srs` |
| `geoip-telegram.srs` | Telegram IP 网段 | `https://github.com/wx2020/sing-box-rules/releases/latest/download/geoip-telegram.srs` |
| `geoip-private.srs` | 私有 / 局域网 IP | `https://github.com/wx2020/sing-box-rules/releases/latest/download/geoip-private.srs` |

> 💡 **全量规则包下载**：
> - 所有 Geosite 规则合集：`geosite-srs.zip`
> - 所有 GeoIP 规则合集：`geoip-srs.zip`
> - 完整合集：`rules-srs.zip`

### 2. GitHub Raw 分支引用渠道

* **Geosite 规则分支**：[`rule-set-geosite`](https://github.com/wx2020/sing-box-rules/tree/rule-set-geosite)
  ```text
  https://raw.githubusercontent.com/wx2020/sing-box-rules/rule-set-geosite/geosite-<名称>.srs
  ```
* **GeoIP 规则分支**：[`rule-set-geoip`](https://github.com/wx2020/sing-box-rules/tree/rule-set-geoip)
  ```text
  https://raw.githubusercontent.com/wx2020/sing-box-rules/rule-set-geoip/geoip-<国家代码或名称>.srs
  ```

### 3. jsDelivr CDN 加速渠道 (国内推荐)

如果您的客户端在拉取 GitHub 原生链接存在网络困难，可通过 jsDelivr CDN 加速：

* **Geosite**：
  ```text
  https://cdn.jsdelivr.net/gh/wx2020/sing-box-rules@rule-set-geosite/geosite-cn.srs
  ```
* **GeoIP**：
  ```text
  https://cdn.jsdelivr.net/gh/wx2020/sing-box-rules@rule-set-geoip/geoip-cn.srs
  ```

---

## 🛠️ 项目构建原理

1. **同步检测**：通过 GitHub API 对比上游 `Loyalsoldier/v2ray-rules-dat` 与 `Loyalsoldier/geoip` 最新 Release 标签。
2. **规则解码与优化**：
   - 解析上游 Protobuf 数据与 MaxMind 数据库。
   - 过滤与清洗：剔除无效规则、去除无意义的多余前缀与重复项。
   - 聚合降维：子域自动合并入父级后缀，CIDR 自动整合为最小前缀集。
3. **SRS 序列化**：使用 sing-box 官方 `srs.Write` 以最高压缩等级编译为 Version 3 二进制规则集。
4. **多渠道推送**：自动打包并上传至 Releases 资产库，同时推送到独立分支。

---

## 📄 开源鸣谢与许可

- 上游规则数据源：
  - [Loyalsoldier/v2ray-rules-dat](https://github.com/Loyalsoldier/v2ray-rules-dat)
  - [Loyalsoldier/geoip](https://github.com/Loyalsoldier/geoip)
- 核心引擎与库：
  - [sing-box](https://github.com/SagerNet/sing-box) by SagerNet
- 许可协议：[MIT License](LICENSE)
