# OCI Reserved IP Bot

Telegram Bot 管理 OCI 预留 IP。

## 配置

复制并编辑配置文件：
```bash
cp conf.example conf
vim conf
```

配置文件包含所有设置（OCI + Telegram）：
```
oci_tenancy=ocid1.tenancy.oc1..xxx
oci_user=ocid1.user.oc1..xxx
oci_fingerprint=aa:bb:cc:...
oci_key_file=./oci_api_key.pem
oci_region=ap-singapore-1
oci_compartment_id=ocid1.compartment.oc1..xxx
telegram_bot_token=123456:ABC...
telegram_admin_id=123456789
```

## 运行

```bash
./oci-bot           # 默认读取 ./conf
./oci-bot -c /path/to/conf  # 指定配置文件
```

## 命令

- `/newip` - 创建预留 IP
- `/listip` - 列出所有 IP
- `/delip <IP>` - 删除 IP
- `/id` - 显示你的 Telegram ID
