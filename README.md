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

VPS 自动申请还需要配置以下字段（在账号段内）：
```
vps_ad=xxx:AP-SINGAPORE-1-AD-1
vps_subnet_id=ocid1.subnet.oc1..xxx
vps_image_arm=ocid1.image.oc1..armxxx
vps_image_amd=ocid1.image.oc1..amdxxx
vps_shape_arm=VM.Standard.A1.Flex
vps_shape_amd=VM.Standard.E4.Flex
vps_ocpus_arm=1
vps_memory_gb_arm=6
vps_ocpus_amd=1
vps_memory_gb_amd=1
vps_ssh_keys=ssh-rsa AAAA... user@host
vps_boot_volume_gb=50
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
- `/checkip <IP>` - 检测 IP 纯净度
- `/autoip` - 自动刷 IP
- `/stopauto` - 停止自动刷 IP
- `/autovps` - 自动申请 VPS
- `/stopvps` - 停止自动申请 VPS
- `/id` - 显示你的 Telegram ID
