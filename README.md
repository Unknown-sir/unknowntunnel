# Unknowntunnel

تونل امن و مستقل برای انتقال ترافیک **لایه ۳** و **لایه ۴** بین دو سرور لینوکسی، با پشتیبانی واقعی از TCP و UDP.

Unknowntunnel یک رابط `TUN` برای انتقال بسته‌های IP ایجاد می‌کند و هم‌زمان می‌تواند پورت‌های TCP و UDP را بدون وابستگی به HAProxy، socat یا تغییر DNS سیستم به سرویس‌های مجاز سمت مقابل متصل کند.

> نسخه فعلی: `0.1.0`  
> سیستم‌عامل: Linux با systemd؛ قابلیت TUN برای لایه ۳  
> معماری‌های آماده: `amd64`، `arm64` و `armv7`

## ویژگی‌ها

- تونل لایه ۳ با رابط Linux TUN
- انتخاب بسته‌های داخلی: فقط TCP، فقط UDP یا هر دو
- فوروارد لایه ۴ برای پورت‌های TCP و UDP
- انتقال روی TCP، UDP یا هر دو مسیر
- رمزنگاری AES-256-GCM برای تمام داده‌ها
- احراز هویت دو سمت با secret مشترک
- secret خارج از آرگومان process و داخل فایل با مجوز `0600`
- انتقال UDP با ACK، بازفرست، تشخیص بسته تکراری و تحویل مرتب
- fragmentation و reassembly برای دیتاگرام‌های UDP تا 65507 بایت
- حالت `both` با اتصال هم‌زمان TCP و UDP، مسیر ترجیحی و failover خودکار
- اتصال مجدد خودکار پس از قطع ارتباط
- سرویس systemd مستقل برای هر instance
- امکان اجرای چند تونل با پورت، subnet و interface مستقل
- allowlist سرویس‌های مقصد؛ پروژه به open proxy تبدیل نمی‌شود
- بدون تغییر خودکار DNS، sysctl، iptables یا تنظیمات سرویس‌های دیگر
- بدون متوقف‌کردن HAProxy، socat یا processهای نامرتبط
- اعتبارسنجی کامل فایل تنظیمات پیش از اجرا
- باینری‌های prebuilt همراه checksum

---

## معماری

### لایه ۳

در هر دو سرور یک interface از نوع TUN ساخته می‌شود:

```text
Application TCP/UDP
        ↓
Linux IP stack
        ↓
TUN interface (Layer 3)
        ↓
Unknowntunnel encrypted transport
        ↓
TCP and/or UDP over Internet
        ↓
Peer TUN interface
```

بسته داخل TUN یک بسته IP است. گزینه `l3.allow_protocols` تعیین می‌کند کدام protocolهای داخلی عبور کنند:

```json
"allow_protocols": ["tcp"]
```

```json
"allow_protocols": ["udp"]
```

```json
"allow_protocols": ["tcp", "udp"]
```

Unknowntunnel در این نسخه ICMP را از رابط TUN عبور نمی‌دهد؛ بنابراین برای تست مسیر لایه ۳ از اتصال TCP یا UDP استفاده کنید، نه `ping`.

### لایه ۴

در سمت ورودی یک listener محلی ساخته می‌شود. هر forward به نام یک service مجاز در سمت مقابل اشاره می‌کند:

```text
Internet client
      ↓
Iran server :443
      ↓
Unknowntunnel TCP forward
      ↓
Encrypted tunnel
      ↓
Kharej service 127.0.0.1:443
```

برای UDP نیز مرز هر datagram حفظ می‌شود:

```text
UDP client
    ↓
Iran server :5353/udp
    ↓
Encrypted tunnel
    ↓
Kharej service 127.0.0.1:53/udp
```

مقصد خام از سمت ورودی ارسال نمی‌شود. سمت خروجی فقط serviceهایی را قبول می‌کند که در بخش `services` فایل تنظیمات خودش تعریف شده باشند.

---

## حالت‌های انتقال

گزینه `transport.mode` مسیر بیرونی تونل را تعیین می‌کند:

| مقدار | رفتار |
|---|---|
| `tcp` | همه پیام‌ها روی اتصال TCP رمز‌شده منتقل می‌شوند. |
| `udp` | همه پیام‌ها روی transport UDP قابل‌اعتماد و رمز‌شده منتقل می‌شوند. |
| `both` | هر دو اتصال آماده می‌مانند؛ مسیر ترجیحی ابتدا استفاده می‌شود و اگر همان فراخوانی ارسال خطا بدهد، پیام روی مسیر دوم نیز امتحان می‌شود. |

حالت `both` برای تحمل خرابی مناسب است و برخلاف ارسال تکراری، هر پیام را فقط روی یک مسیر می‌فرستد. برای اجبار به یک حامل مشخص، `tcp` یا `udp` را انتخاب کنید.

گزینه `transport.prefer` مسیر اصلی را مشخص می‌کند. در حالت `both` هر دو session برقرار می‌مانند؛ مسیر ترجیحی استفاده می‌شود و در صورت خطا، همان پیام از مسیر دوم ارسال می‌شود.

### تفاوت transport و packet type

دو تنظیم مستقل وجود دارد:

- `transport.mode`: تونل روی اینترنت با TCP، UDP یا هر دو برقرار شود.
- `l3.allow_protocols`: داخل تونل بسته‌های TCP، UDP یا هر دو مجاز باشند.

مثال: می‌توانید transport را روی UDP قرار دهید ولی داخل آن هم TCP و هم UDP را عبور دهید.

---

## پیش‌نیازها

هر دو سرور باید موارد زیر را داشته باشند:

- Linux
- systemd
- دسترسی root
- ابزار `ip` از بسته `iproute2`
- دستگاه `/dev/net/tun` برای فعال‌کردن لایه ۳؛ در حالت صرفاً لایه ۴ می‌توان `l3.enabled` را `false` گذاشت.
- دسترسی شبکه بین دو سرور روی پورت انتخاب‌شده

برای استفاده از لایه ۳، TUN را بررسی کنید:

```bash
ls -l /dev/net/tun
```

اگر فایل وجود ندارد، ماژول TUN را فعال کنید:

```bash
sudo modprobe tun
```

برای بارگذاری پس از reboot:

```bash
echo tun | sudo tee /etc/modules-load.d/tun.conf
```

---

## نصب

پروژه را روی هر دو سرور دریافت و استخراج کنید. برای فایل ZIP:

```bash
unzip Unknowntunnel-0.1.0.zip
cd Unknowntunnel
sudo ./scripts/install.sh
```

پس از قرارگرفتن پروژه در مخزن، نصب مستقیم از سورس نیز به این شکل است:

```bash
git clone https://github.com/Unknown-sir/Unknowntunnel.git
cd Unknowntunnel
sudo ./scripts/install.sh
```

نصب‌کننده:

- checksum باینری مناسب معماری را بررسی می‌کند؛
- باینری را در `/usr/local/bin/unknowntunnel` قرار می‌دهد؛
- unit template را در systemd نصب می‌کند؛
- مسیر `/etc/unknowntunnel` را با مجوز محدود می‌سازد؛
- هیچ tunnel instance را بدون فایل تنظیمات فعال نمی‌کند؛
- DNS، firewall، sysctl یا سرویس‌های دیگر را تغییر نمی‌دهد.

بررسی نسخه:

```bash
unknowntunnel version
```

---

## ساخت secret مشترک

Secret را فقط روی یکی از سرورها تولید کنید:

```bash
sudo unknowntunnel keygen -out /etc/unknowntunnel/secret.key
```

فایل ایجادشده را از یک مسیر امن به سرور دوم منتقل کنید:

```bash
sudo scp /etc/unknowntunnel/secret.key root@SECOND_SERVER:/etc/unknowntunnel/secret.key
```

روی هر دو سرور مجوز فایل را بررسی کنید:

```bash
sudo chown root:root /etc/unknowntunnel/secret.key
sudo chmod 600 /etc/unknowntunnel/secret.key
```

دو سمت باید دقیقاً secret یکسان داشته باشند. از مقدار کوتاه، قابل حدس یا پیش‌فرض استفاده نکنید.

---

# راه‌اندازی نمونه بین ایران و خارج

در این مثال:

```text
Iran tunnel IP:     10.77.0.1/30
Kharej tunnel IP:   10.77.0.2/30
Transport port:     8443 TCP + UDP
```

## تنظیم سرور خارج

نمونه را کپی کنید:

```bash
sudo cp /usr/share/doc/unknowntunnel/server.json.example /etc/unknowntunnel/server.json
sudo nano /etc/unknowntunnel/server.json
```

نمونه کامل:

```json
{
  "node_id": "kharej",
  "peer_id": "iran",
  "role": "server",
  "auth": {
    "secret_file": "/etc/unknowntunnel/secret.key"
  },
  "transport": {
    "mode": "both",
    "prefer": "udp",
    "listen_tcp": "0.0.0.0:8443",
    "listen_udp": "0.0.0.0:8443",
    "connect_tcp": "",
    "connect_udp": ""
  },
  "l3": {
    "enabled": true,
    "interface": "utun0",
    "address": "10.77.0.2/30",
    "mtu": 1200,
    "routes": [],
    "allow_protocols": ["tcp", "udp"]
  },
  "services": {
    "web-https": {
      "network": "tcp",
      "address": "127.0.0.1:443"
    },
    "dns-udp": {
      "network": "udp",
      "address": "127.0.0.1:53"
    }
  },
  "forwards": []
}
```

در بخش `services` فقط مقصدهایی را تعریف کنید که باید از سمت ایران قابل دسترسی باشند.

## تنظیم سرور ایران

نمونه را کپی کنید:

```bash
sudo cp /usr/share/doc/unknowntunnel/client.json.example /etc/unknowntunnel/client.json
sudo nano /etc/unknowntunnel/client.json
```

مقدار `KHAREJ_PUBLIC_IP` را با IP عمومی واقعی سرور خارج عوض کنید:

```json
{
  "node_id": "iran",
  "peer_id": "kharej",
  "role": "client",
  "auth": {
    "secret_file": "/etc/unknowntunnel/secret.key"
  },
  "transport": {
    "mode": "both",
    "prefer": "udp",
    "listen_tcp": "",
    "listen_udp": "",
    "connect_tcp": "KHAREJ_PUBLIC_IP:8443",
    "connect_udp": "KHAREJ_PUBLIC_IP:8443"
  },
  "l3": {
    "enabled": true,
    "interface": "utun0",
    "address": "10.77.0.1/30",
    "mtu": 1200,
    "routes": [],
    "allow_protocols": ["tcp", "udp"]
  },
  "services": {},
  "forwards": [
    {
      "name": "https-in",
      "protocol": "tcp",
      "listen": "0.0.0.0:443",
      "service": "web-https"
    },
    {
      "name": "dns-in",
      "protocol": "udp",
      "listen": "0.0.0.0:5353",
      "service": "dns-udp"
    }
  ]
}
```

نام `service` در forward سمت ایران باید دقیقاً با نام service تعریف‌شده در سرور خارج یکسان باشد.

---

## بررسی فایل تنظیمات

قبل از start، روی هر دو سرور اجرا کنید:

```bash
sudo unknowntunnel check -config /etc/unknowntunnel/server.json
```

یا در سمت ایران:

```bash
sudo unknowntunnel check -config /etc/unknowntunnel/client.json
```

در صورت وجود فیلد ناشناخته، IP نامعتبر، mode اشتباه، interface طولانی، secret کوتاه یا پورت ناقص، برنامه اجرا نمی‌شود.

---

## تنظیم firewall

در سمت server باید transport port باز باشد.

برای حالت `both`:

```bash
sudo ufw allow 8443/tcp
sudo ufw allow 8443/udp
```

برای حالت `tcp` فقط TCP و برای حالت `udp` فقط UDP را باز کنید.

در سمت ایران نیز پورت‌های عمومی forward را بر اساس نیاز باز کنید:

```bash
sudo ufw allow 443/tcp
sudo ufw allow 5353/udp
```

بهتر است transport port سرور خارج فقط برای IP عمومی سرور ایران مجاز باشد:

```bash
sudo ufw allow from IRAN_PUBLIC_IP to any port 8443 proto tcp
sudo ufw allow from IRAN_PUBLIC_IP to any port 8443 proto udp
```

Unknowntunnel خودش ruleهای firewall را تغییر نمی‌دهد.

---

## اجرای سرویس‌ها

ابتدا سرور خارج:

```bash
sudo systemctl enable --now unknowntunnel@server
```

سپس سرور ایران:

```bash
sudo systemctl enable --now unknowntunnel@client
```

وضعیت:

```bash
sudo systemctl status unknowntunnel@server
sudo systemctl status unknowntunnel@client
```

لاگ زنده:

```bash
sudo journalctl -u unknowntunnel@server -f
```

```bash
sudo journalctl -u unknowntunnel@client -f
```

در حالت `both` باید پیام آماده‌شدن sessionهای TCP و UDP را ببینید:

```text
authenticated tcp tunnel session is ready
authenticated udp tunnel session is ready
```

---

# تنظیمات لایه ۳

## فقط TCP داخلی

روی هر دو سرور:

```json
"allow_protocols": ["tcp"]
```

## فقط UDP داخلی

```json
"allow_protocols": ["udp"]
```

## TCP و UDP داخلی

```json
"allow_protocols": ["tcp", "udp"]
```

تنظیم دو سمت باید یکسان باشد تا رفتار قابل پیش‌بینی باشد.

## Route کردن یک subnet پشت سرور مقابل

فرض کنید پشت سرور خارج شبکه `172.16.20.0/24` قرار دارد. در سمت ایران:

```json
"routes": ["172.16.20.0/24"]
```

Unknowntunnel route را فقط روی interface خودش اضافه می‌کند. برای تبدیل سرور خارج به router باید forwarding سیستم را آگاهانه فعال کنید:

```bash
sudo tee /etc/sysctl.d/90-unknowntunnel-forward.conf >/dev/null <<'SYSCTL'
net.ipv4.ip_forward=1
SYSCTL
sudo sysctl --system
```

همچنین شبکه مقصد باید route برگشت به subnet تونل داشته باشد. اگر route برگشت ندارید، می‌توانید NAT را خودتان با firewall مدیریت کنید. پروژه عمداً NAT و iptables را خودکار تغییر نمی‌دهد.

## MTU

مقدار پیشنهادی:

```json
"mtu": 1200
```

بازه مجاز `576` تا `1300` است. MTU پایین‌تر احتمال fragmentation در اینترنت را کم می‌کند. تغییر به مقدار بالاتر از `1300` توسط validator رد می‌شود.

---

# تنظیمات لایه ۴

## تعریف service سمت خروجی

TCP:

```json
"my-web": {
  "network": "tcp",
  "address": "127.0.0.1:8080"
}
```

UDP:

```json
"my-game": {
  "network": "udp",
  "address": "127.0.0.1:27015"
}
```

مقصد می‌تواند loopback، IP خصوصی یا IP دیگری باشد که از سرور خروجی قابل دسترسی است.

## تعریف forward سمت ورودی

TCP:

```json
{
  "name": "web-public",
  "protocol": "tcp",
  "listen": "0.0.0.0:8080",
  "service": "my-web"
}
```

UDP:

```json
{
  "name": "game-public",
  "protocol": "udp",
  "listen": "0.0.0.0:27015",
  "service": "my-game"
}
```

برای محدودکردن listener به یک IP خاص:

```json
"listen": "203.0.113.10:8080"
```

یا فقط روی loopback:

```json
"listen": "127.0.0.1:8080"
```

## تست TCP forward

اگر service سمت خارج HTTP است:

```bash
curl -v http://IRAN_PUBLIC_IP:8080/
```

## تست UDP forward

با `netcat` یا ابزار مناسب protocol مقصد:

```bash
echo test | nc -u -w2 IRAN_PUBLIC_IP 5353
```

برای DNS:

```bash
dig @IRAN_PUBLIC_IP -p 5353 example.com
```

---

# اجرای چند تونل روی یک سرور

برای هر instance موارد زیر باید مستقل باشند:

- نام فایل config
- `node_id`
- transport port
- TUN interface
- subnet لایه ۳
- public forward ports

مثال:

```text
/etc/unknowntunnel/client-a.json
/etc/unknowntunnel/client-b.json
```

Instance اول:

```json
"interface": "utun0",
"address": "10.77.0.1/30",
"connect_tcp": "198.51.100.10:8443",
"connect_udp": "198.51.100.10:8443"
```

Instance دوم:

```json
"interface": "utun1",
"address": "10.77.0.5/30",
"connect_tcp": "198.51.100.20:8444",
"connect_udp": "198.51.100.20:8444"
```

اجرای هم‌زمان:

```bash
sudo systemctl enable --now unknowntunnel@client-a
sudo systemctl enable --now unknowntunnel@client-b
```

هیچ RPC port ثابت، service name مشترک یا config سراسری بین instanceها وجود ندارد.

---

# مرجع فایل تنظیمات

## فیلدهای اصلی

| فیلد | توضیح |
|---|---|
| `node_id` | شناسه این سمت؛ حداکثر 64 کاراکتر امن |
| `peer_id` | شناسه مورد انتظار سمت مقابل |
| `role` | `server` یا `client` |
| `auth.secret_file` | مسیر فایل secret مشترک |

## transport

| فیلد | توضیح |
|---|---|
| `mode` | `tcp`، `udp` یا `both` |
| `prefer` | `tcp` یا `udp` |
| `listen_tcp` | آدرس listen در role سرور |
| `listen_udp` | آدرس listen در role سرور |
| `connect_tcp` | آدرس سرور در role کلاینت |
| `connect_udp` | آدرس سرور در role کلاینت |

در role سرور، `connect_*` می‌تواند خالی باشد. در role کلاینت، `listen_*` می‌تواند خالی باشد.

## l3

| فیلد | توضیح |
|---|---|
| `enabled` | فعال یا غیرفعال‌کردن TUN لایه ۳ |
| `interface` | نام interface؛ حداکثر 15 کاراکتر |
| `address` | IP/CIDR این سمت |
| `mtu` | بین 576 و 1300 |
| `routes` | routeهایی که از TUN عبور می‌کنند |
| `allow_protocols` | `tcp`، `udp` یا هر دو |

## services

Map از نام service به:

| فیلد | توضیح |
|---|---|
| `network` | `tcp` یا `udp` |
| `address` | مقصد واقعی قابل دسترسی از این سرور |

## forwards

| فیلد | توضیح |
|---|---|
| `name` | نام یکتای forward |
| `protocol` | `tcp` یا `udp` |
| `listen` | IP و پورت listener محلی |
| `service` | نام service مجاز سمت مقابل |

---

# امنیت

Unknowntunnel از مدل زیر استفاده می‌کند:

1. هر peer شناسه خودش و شناسه مورد انتظار سمت مقابل را اعلام می‌کند.
2. handshake با HMAC-SHA256 و secret مشترک احراز می‌شود.
3. دو nonce تصادفی برای هر session تولید می‌شود.
4. کلیدهای مجزای ارسال، دریافت و ACK با HKDF-SHA256 مشتق می‌شوند.
5. داده‌ها با AES-256-GCM رمزنگاری و authenticate می‌شوند.
6. sequence number و replay protection از پذیرش مجدد frame جلوگیری می‌کند.
7. کلیدهای جهت رفت و برگشت متفاوت هستند.

توصیه‌ها:

- secret تولیدشده توسط `keygen` را استفاده کنید.
- secret را در command line یا repository قرار ندهید.
- مجوز فایل secret را `0600` نگه دارید.
- transport port را با firewall به IP peer محدود کنید.
- فقط serviceهای ضروری را در allowlist قرار دهید.
- سرویس را با آخرین نسخه سیستم‌عامل اجرا کنید.
- ساعت دو سرور را با NTP همگام نگه دارید؛ اختلاف بیش از پنج دقیقه handshake را رد می‌کند.

این پروژه جایگزین firewall، access control برنامه مقصد یا به‌روزرسانی امنیتی سیستم‌عامل نیست. طراحی رمزنگاری نسخه فعلی ممیزی مستقل رسمی نشده است؛ برای محیط‌های بسیار حساس، بازبینی امنیتی و آزمون عملی مستقل انجام دهید.

جزئیات wire protocol و key derivation در فایل [`docs/PROTOCOL.md`](docs/PROTOCOL.md) نوشته شده است.

---

# عیب‌یابی

## خطای `/dev/net/tun is missing`

```bash
sudo modprobe tun
ls -l /dev/net/tun
```

در VPSهایی که provider اجازه TUN نمی‌دهد باید این قابلیت از پنل provider فعال شود.

## خطای `peer clock differs by more than five minutes`

زمان هر دو سرور را بررسی کنید:

```bash
timedatectl status
```

NTP را فعال کنید:

```bash
sudo timedatectl set-ntp true
```

## session TCP برقرار می‌شود ولی UDP نه

- UDP transport port را در firewall باز کنید.
- Security Group دیتاسنتر را بررسی کنید.
- مطمئن شوید هر دو سمت `mode: both` یا `mode: udp` دارند.
- `connect_udp` و `listen_udp` باید پورت یکسان داشته باشند.
- NAT یا provider ممکن است UDP را محدود کرده باشد.

## session UDP برقرار می‌شود ولی بعداً قطع می‌شود

UDP transport برای packetهای بدون ACK بازفرست انجام می‌دهد. اگر peer پس از چند تلاش پاسخ ندهد session بسته و دوباره ساخته می‌شود. لاگ هر دو سمت و packet loss مسیر را بررسی کنید.

## خطای identity mismatch

مقادیر باید معکوس باشند:

```text
Server node_id = kharej
Server peer_id = iran

Client node_id = iran
Client peer_id = kharej
```

## خطای handshake authentication

secret دو سمت متفاوت است. checksum فایل را مقایسه کنید:

```bash
sudo sha256sum /etc/unknowntunnel/secret.key
```

## TCP forward باز نمی‌شود

- نام service را در هر دو config بررسی کنید.
- service سمت خروجی باید `network: tcp` داشته باشد.
- مقصد service را روی سرور خروجی مستقیم تست کنید.
- لاگ `remote service connection failed` را بررسی کنید.

## UDP پاسخ ندارد

- service سمت خروجی باید `network: udp` باشد.
- برنامه مقصد باید پاسخ را به همان socket برگرداند.
- firewall هر دو سرور و firewall برنامه مقصد را بررسی کنید.
- مدت idle هر UDP flow دو دقیقه است و بعد از آن flow جدید ساخته می‌شود.

## interface ساخته شده ولی ترافیک subnet عبور نمی‌کند

Unknowntunnel route را ایجاد می‌کند، اما forwarding و NAT را عمداً خودکار فعال نمی‌کند. موارد زیر را بررسی کنید:

- `net.ipv4.ip_forward=1`
- route برگشت شبکه مقصد
- firewall FORWARD
- NAT در صورت نبود route برگشت

## مشاهده interface و route

```bash
ip addr show utun0
ip route show dev utun0
```

## بررسی پورت‌های listen

```bash
ss -lntup | grep -E '8443|unknowntunnel'
```

---

# دستورات مدیریتی

اعتبارسنجی:

```bash
sudo unknowntunnel check -config /etc/unknowntunnel/client.json
```

اجرای مستقیم برای debug:

```bash
sudo unknowntunnel run -config /etc/unknowntunnel/client.json
```

Restart:

```bash
sudo systemctl restart unknowntunnel@client
```

Stop:

```bash
sudo systemctl stop unknowntunnel@client
```

غیرفعال‌کردن اجرا پس از reboot:

```bash
sudo systemctl disable unknowntunnel@client
```

لاگ‌های 100 خط اخیر:

```bash
sudo journalctl -u unknowntunnel@client -n 100 --no-pager
```

---

# حذف

حذف برنامه و unit با حفظ تنظیمات:

```bash
cd Unknowntunnel
sudo ./scripts/uninstall.sh
```

حذف همراه با فایل‌های `/etc/unknowntunnel`:

```bash
sudo ./scripts/uninstall.sh --purge
```

---

# محدودیت‌های نسخه فعلی

- فقط Linux پشتیبانی می‌شود.
- هر config instance فقط یک peer مشخص را authenticate می‌کند.
- ICMP در لایه ۳ عبور داده نمی‌شود.
- حالت `both` دو session آماده نگه می‌دارد، اما هر پیام را تنها روی مسیر اصلی یا مسیر failover می‌فرستد. بسته‌ای که پیش از تشخیص قطعی در مسیر قبلی بوده ممکن است از دست برود و اتصال TCP برنامه به reconnect نیاز داشته باشد.
- UDP transport برای تحویل قابل‌اعتماد مرتب شده و در packet loss بالا ممکن است latency بیشتری از UDP خام داشته باشد.
- NAT، DNS، iptables و sysctl به‌طور خودکار تغییر نمی‌کنند.
- تغییر config نیازمند restart همان instance است.
- این نسخه اولیه است و قبل از استفاده حساس باید در محیط واقعی خودتان load test، failover test و security review انجام دهید.

---

## توسعه از سورس

نیازمند Go 1.23 یا جدیدتر:

```bash
go test ./...
go vet ./...
make build
```

ساخت باینری‌های Linux:

```bash
VERSION=0.1.0 ./scripts/build-release.sh
```

