# Unknowntunnel

تونل رمزنگاری‌شده لایه ۳ و لایه ۴ برای Linux، با پشتیبانی واقعی از TCP و UDP و پنل تعاملی داخل ترمینال.

Unknowntunnel می‌تواند بین دو سرور یک رابط `TUN` لایه ۳ ایجاد کند و بسته‌های IP را منتقل کند. همچنین بدون نیاز به HAProxy یا socat، فورواردهای لایه ۴ TCP و UDP را از یک سمت به سرویس‌های مجاز سمت دیگر می‌رساند.

> نسخه فعلی: `0.2.0`  
> سیستم‌عامل: Linux با systemd  
> معماری‌های آماده: `amd64`، `arm64` و `armv7`

## امکانات اصلی

- پنل تعاملی ترمینال برای ساخت و مدیریت تونل
- ایجاد خودکار فایل تنظیمات و secret با دسترسی محدود
- ایجاد و مدیریت خودکار instanceهای systemd
- Start، Stop، Restart، Status و Logs از داخل پنل
- ویرایش و حذف امن تونل‌ها
- تونل لایه ۳ مبتنی بر Linux TUN
- انتخاب بسته‌های داخلی لایه ۳:
  - فقط TCP
  - فقط UDP
  - TCP و UDP
- فوروارد لایه ۴ برای TCP و UDP
- carrier بیرونی قابل انتخاب:
  - TCP
  - UDP
  - هر دو با مسیر ترجیحی و failover
- انتقال UDP با حفظ مرز datagram
- fragmentation و reassembly برای دیتاگرام‌های بزرگ UDP
- ACK، retransmission و duplicate detection روی carrier نوع UDP
- رمزنگاری AES-256-GCM
- احراز هویت دو سمت با secret مشترک
- اتصال مجدد خودکار
- پشتیبانی از چند تونل مستقل روی یک سرور
- allowlist برای مقصدهای سمت مقابل
- بدون تغییر خودکار DNS، iptables، sysctl یا سرویس‌های نامرتبط
- بدون متوقف‌کردن HAProxy یا socat موجود روی سرور

---

## معماری پروژه

### لایه ۳

در هر سمت یک interface از نوع TUN ساخته می‌شود:

```text
Application TCP/UDP
        ↓
Linux IP stack
        ↓
TUN interface — Layer 3
        ↓
Unknowntunnel encrypted transport
        ↓
TCP and/or UDP over Internet
        ↓
Peer TUN interface
```

بسته داخل TUN یک بسته IP است. کاربر هنگام ساخت تونل از پنل مشخص می‌کند که بسته‌های داخلی TCP، UDP یا هر دو عبور کنند.

در نسخه فعلی ICMP از TUN عبور داده نمی‌شود. برای تست لایه ۳ از اتصال TCP یا UDP استفاده کنید، نه `ping`.

### لایه ۴

سمت ورودی یک listener محلی ایجاد می‌کند و آن را به نام یک service مجاز در سمت مقابل متصل می‌کند:

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

UDP نیز به همین روش منتقل می‌شود و مرز هر datagram حفظ می‌شود:

```text
UDP client
    ↓
Iran server :5353/udp
    ↓
Encrypted tunnel
    ↓
Kharej service 127.0.0.1:53/udp
```

سمت ورودی نمی‌تواند هر مقصد دلخواهی را باز کند. مقصد باید قبلاً با یک نام در بخش serviceهای سمت مقابل تعریف شده باشد.

---

## تفاوت سه انتخاب TCP و UDP

در تنظیمات سه مفهوم مستقل وجود دارد:

### ۱. نوع carrier بیرونی

مشخص می‌کند ارتباط رمزنگاری‌شده بین دو سرور روی چه پروتکلی برقرار شود:

| حالت | توضیح |
|---|---|
| `tcp` | همه پیام‌های تونل روی اتصال TCP عبور می‌کنند. |
| `udp` | همه پیام‌ها روی carrier قابل‌اعتماد UDP عبور می‌کنند. |
| `both` | هر دو مسیر متصل می‌مانند؛ مسیر ترجیحی استفاده می‌شود و هنگام خطای قابل تشخیص، ارسال روی مسیر دوم انجام می‌شود. |

در حالت `both` گزینه Preferred Transport تعیین می‌کند ابتدا TCP یا UDP استفاده شود.

### ۲. نوع بسته داخلی لایه ۳

مشخص می‌کند داخل TUN چه packetهایی مجاز باشند:

- `tcp`
- `udp`
- `both`

برای نمونه، carrier بیرونی می‌تواند UDP باشد ولی داخل آن هم بسته‌های TCP و هم UDP منتقل شوند.

### ۳. پروتکل هر فوروارد لایه ۴

هر forward به‌صورت جداگانه TCP یا UDP است. مقصد remote service نیز باید همان protocol را داشته باشد.

---

## پیش‌نیازها

روی هر دو سرور:

- Linux
- systemd
- دسترسی root
- `iproute2`
- `sha256sum`
- دسترسی شبکه بین دو سرور روی پورت انتخابی
- `/dev/net/tun` برای قابلیت لایه ۳

بررسی TUN:

```bash
ls -l /dev/net/tun
```

در صورت نبودن آن:

```bash
sudo modprobe tun
```

برای بارگذاری پس از reboot:

```bash
echo tun | sudo tee /etc/modules-load.d/tun.conf
```

اگر فقط فوروارد لایه ۴ لازم است، می‌توان Layer 3 را در پنل غیرفعال کرد.

---

# نصب

فایل پروژه را استخراج کنید و وارد پوشه شوید:

```bash
git clone https://github.com/Unknown-sir/unknowntunnel.git
cd Unknowntunnel
sudo ./scripts/install.sh
```

نصب‌کننده:

- باینری مناسب معماری را انتخاب می‌کند؛
- checksum باینری را بررسی می‌کند؛
- برنامه را در `/usr/local/bin/unknowntunnel` نصب می‌کند؛
- systemd template را نصب می‌کند؛
- مسیر `/etc/unknowntunnel` را با permission محدود ایجاد می‌کند؛
- در ترمینال تعاملی پیشنهاد می‌دهد پنل تنظیمات همان لحظه باز شود؛
- DNS، firewall، sysctl و سرویس‌های دیگر را تغییر نمی‌دهد.

برای نصب غیرتعاملی و باز نشدن پنل:

```bash
sudo UNKNOWNTUNNEL_NO_MENU=1 ./scripts/install.sh
```

بررسی نسخه:

```bash
unknowntunnel version
```

---

# پنل مدیریت ترمینال

برای بازکردن پنل:

```bash
sudo unknowntunnel
```

یا:

```bash
sudo unknowntunnel menu
```

منوی اصلی امکانات زیر را دارد:

```text
1) Create a tunnel
2) Edit a tunnel
3) List tunnels
4) Start a tunnel
5) Stop a tunnel
6) Restart a tunnel
7) Enable a tunnel at boot
8) Disable a tunnel at boot
9) Show tunnel status
10) Show recent tunnel logs
11) Show tunnel configuration
12) Delete a tunnel
13) Validate all configurations
0) Exit
```

پنل هنگام ساخت تونل این اطلاعات را دریافت می‌کند:

- نام instance
- نقش server یا client
- شناسه local node و peer
- نوع carrier: TCP، UDP یا هر دو
- IP و پورت listener یا peer
- روش ساخت یا دریافت secret مشترک
- فعال یا غیرفعال بودن Layer 3
- نام TUN interface
- IP داخلی تونل
- MTU
- نوع بسته‌های داخلی TCP/UDP
- routeهای اختیاری
- remote serviceها
- local forwardهای TCP و UDP

در پایان:

1. تنظیمات اعتبارسنجی می‌شوند.
2. فایل JSON با permission برابر `0600` ذخیره می‌شود.
3. secret با permission برابر `0600` ذخیره می‌شود.
4. systemd reload می‌شود.
5. با تأیید کاربر، سرویس به‌صورت خودکار enable و start می‌شود.

---

# راه‌اندازی نمونه ایران و خارج

در این مثال:

```text
Iran tunnel address:     10.77.0.1/30
Kharej tunnel address:   10.77.0.2/30
Transport port:          8443
Transport mode:          both
Preferred carrier:       udp
```

## مرحله اول: سرور خارج

پنل را اجرا کنید:

```bash
sudo unknowntunnel
```

گزینه `Create a tunnel` را انتخاب کنید و مقادیر پیشنهادی زیر را وارد کنید:

```text
Instance name: tunnel1
Role: server
Local node ID: kharej
Peer node ID: iran
Outer transport: both
Preferred transport: udp
Listen address: 0.0.0.0
Transport port: 8443
Use separate TCP and UDP ports: no
Shared secret action: generate
Enable Layer 3 TUN: yes
TUN interface: utun0
Local tunnel address: 10.77.0.2/30
TUN MTU: 1200
Layer 3 packet types: both
Routes: blank
```

Secret تولیدشده یک بار نمایش داده می‌شود. آن را از یک مسیر امن برای تنظیم سرور ایران نگه دارید.

### تعریف service در سرور خارج

اگر قرار است HTTPS سرور خارج از طریق ایران قابل دسترسی باشد:

```text
Configure destination services: yes
Number of destination services: 1
Service name: web-https
Service protocol: tcp
Destination address: 127.0.0.1:443
```

نمونه UDP برای DNS:

```text
Service name: dns-udp
Service protocol: udp
Destination address: 127.0.0.1:53
```

در پایان start خودکار را تأیید کنید.

## مرحله دوم: سرور ایران

پنل را اجرا کنید:

```bash
sudo unknowntunnel
```

مقادیر پیشنهادی:

```text
Instance name: tunnel1
Role: client
Local node ID: iran
Peer node ID: kharej
Outer transport: both
Preferred transport: udp
Peer public IP or hostname: IP_PUBLIC_KHAREJ
Peer transport port: 8443
Use a separate UDP peer port: no
Shared secret action: paste
Enable Layer 3 TUN: yes
TUN interface: utun0
Local tunnel address: 10.77.0.1/30
TUN MTU: 1200
Layer 3 packet types: both
Routes: blank
```

در بخش secret همان مقداری را وارد کنید که سرور خارج تولید کرده است.

### ساخت forward TCP

```text
Configure local Layer 4 forwards: yes
Number of local forwards: 1
Forward name: https-in
Forward protocol: tcp
Local listen address: 0.0.0.0:443
Remote service name: web-https
```

### ساخت forward UDP

برای service با نام `dns-udp`:

```text
Forward name: dns-in
Forward protocol: udp
Local listen address: 0.0.0.0:5353
Remote service name: dns-udp
```

نام Remote Service باید دقیقاً با نام service تعریف‌شده در سرور خارج یکسان باشد.

---

# مدیریت سرویس‌ها

ساده‌ترین روش استفاده از پنل است:

```bash
sudo unknowntunnel
```

دستورات مستقیم نیز در دسترس هستند.

## مشاهده تونل‌ها

```bash
sudo unknowntunnel list
```

## ساخت تونل جدید

```bash
sudo unknowntunnel setup -instance tunnel1
```

## ویرایش تونل

```bash
sudo unknowntunnel edit -instance tunnel1
```

## Start

```bash
sudo unknowntunnel service -instance tunnel1 -action start
```

## Stop

```bash
sudo unknowntunnel service -instance tunnel1 -action stop
```

## Restart

```bash
sudo unknowntunnel service -instance tunnel1 -action restart
```

## Status

```bash
sudo unknowntunnel service -instance tunnel1 -action status
```

## فعال‌سازی در boot

```bash
sudo unknowntunnel service -instance tunnel1 -action enable
```

## غیرفعال‌کردن از boot

```bash
sudo unknowntunnel service -instance tunnel1 -action disable
```

## مشاهده لاگ

```bash
sudo unknowntunnel logs -instance tunnel1
```

دنبال‌کردن زنده لاگ:

```bash
sudo unknowntunnel logs -instance tunnel1 -follow
```

## نمایش تنظیمات

```bash
sudo unknowntunnel show -instance tunnel1
```

## حذف تونل

```bash
sudo unknowntunnel delete -instance tunnel1
```

حذف، سرویس instance را stop و disable می‌کند و فایل تنظیمات را پاک می‌کند. حذف secret به‌صورت جداگانه تأیید می‌شود و اگر tunnel دیگری از همان secret استفاده کند، فایل secret حذف نمی‌شود.

---

# فایل‌ها و مسیرها

```text
/usr/local/bin/unknowntunnel
/etc/systemd/system/unknowntunnel@.service
/etc/unknowntunnel/<instance>.json
/etc/unknowntunnel/<instance>.key
/usr/share/doc/unknowntunnel/
```

هر instance با سرویس زیر اجرا می‌شود:

```text
unknowntunnel@<instance>.service
```

مثال:

```text
/etc/unknowntunnel/tunnel1.json
unknowntunnel@tunnel1.service
```

کاربر برای استفاده عادی نیازی به ساخت دستی این فایل‌ها ندارد؛ پنل آن‌ها را ایجاد و مدیریت می‌کند.

---

# چند تونل روی یک سرور

برای هر اتصال یک instance جدا بسازید:

```text
tunnel1
tunnel2
tunnel3
```

برای جلوگیری از conflict، در هر instance موارد زیر باید مستقل باشند:

- نام instance
- transport port
- TUN interface
- subnet داخلی
- local forward port

نمونه:

```text
tunnel1: port 8443, utun0, 10.77.0.0/30
tunnel2: port 8444, utun1, 10.77.0.4/30
tunnel3: port 8445, utun2, 10.77.0.8/30
```

پنل نام TUN آزاد بعدی را پیشنهاد می‌دهد، ولی مسئولیت انتخاب subnet و پورت بدون تداخل با کاربر است.

---

# Firewall

Unknowntunnel به‌صورت خودکار firewall را تغییر نمی‌دهد.

در سمت server باید transport port انتخاب‌شده باز باشد.

برای حالت `tcp` فقط TCP لازم است. برای حالت `udp` فقط UDP و برای حالت `both` هر دو لازم‌اند.

نمونه UFW:

```bash
sudo ufw allow 8443/tcp
sudo ufw allow 8443/udp
```

در سمت دارای local forward نیز پورت عمومی forward را باز کنید؛ مثلاً:

```bash
sudo ufw allow 443/tcp
sudo ufw allow 5353/udp
```

قواعد firewall را با سیاست امنیتی و IPهای واقعی خود محدود کنید.

---

# Routeهای لایه ۳

پنل می‌تواند routeهای اضافی را به TUN اضافه کند. آن‌ها را به‌صورت comma-separated وارد کنید:

```text
10.20.0.0/16,192.168.50.0/24
```

این قابلیت فقط route محلی را ایجاد می‌کند. برای عبور شبکه پشت سرور باید routing و در صورت نیاز NAT در سیستم‌عامل به‌صورت صحیح طراحی شود. پروژه بدون اجازه کاربر forwarding یا NAT سراسری را فعال نمی‌کند.

---

# امنیت

- برای هر جفت سرور secret قوی و تصادفی استفاده کنید.
- secret دو سمت باید دقیقاً یکسان باشد.
- secret را در پیام‌رسان یا کانال ناامن ارسال نکنید.
- فایل secret با permission برابر `0600` ذخیره می‌شود.
- secret در command line سرویس systemd قرار نمی‌گیرد.
- serviceهای remote فقط از allowlist تنظیمات پذیرفته می‌شوند.
- پورت‌های transport و forward را در firewall محدود کنید.
- سیستم‌عامل و باینری پروژه را به‌روز نگه دارید.
- این پروژه هنوز ممیزی مستقل رسمی رمزنگاری و امنیتی نشده است؛ قبل از استفاده حساس، تست و audit مستقل انجام دهید.

جزئیات بیشتر در فایل `SECURITY.md` و `docs/PROTOCOL.md` قرار دارد.

---

# عیب‌یابی

## سرویس اجرا نمی‌شود

```bash
sudo unknowntunnel service -instance tunnel1 -action status
sudo unknowntunnel logs -instance tunnel1
```

همچنین همه تنظیمات را بررسی کنید:

```bash
sudo unknowntunnel check-all
```

## خطای secret

اطمینان حاصل کنید:

- secret در هر دو سمت یکسان است؛
- طول آن حداقل ۳۲ کاراکتر است؛
- فایل توسط root قابل خواندن است؛
- permission فایل `0600` است.

```bash
sudo chmod 600 /etc/unknowntunnel/tunnel1.key
sudo chown root:root /etc/unknowntunnel/tunnel1.key
```

## اتصال TCP برقرار است ولی UDP نه

- UDP transport port را در firewall هر دو سمت بررسی کنید.
- اگر carrier روی `both` است، لاگ را بررسی کنید که UDP session authenticated شده باشد.
- اگر forward نوع UDP است، remote service نیز باید `udp` باشد.
- بررسی کنید سرویس UDP مقصد روی آدرس تعریف‌شده در حال listen باشد.

```bash
sudo ss -lunp
```

## TUN ساخته نمی‌شود

```bash
ls -l /dev/net/tun
sudo modprobe tun
```

نام interface باید حداکثر ۱۵ کاراکتر و در هر instance منحصربه‌فرد باشد.

## پورت قبلاً استفاده شده است

```bash
sudo ss -lntup
```

transport port و local forward port نباید با سرویس یا tunnel دیگری conflict داشته باشند.

## دو سمت authenticate نمی‌شوند

موارد زیر باید مکمل یکدیگر باشند:

```text
Server node_id = Client peer_id
Server peer_id = Client node_id
```

همچنین secret و transport mode باید در دو سمت سازگار باشند.

---

# اعتبارسنجی و تست توسعه

```bash
go test ./...
go test -race ./...
go vet ./...
```

ساخت باینری‌های release:

```bash
./scripts/build-release.sh
```

---

# حذف برنامه

حذف برنامه با نگه‌داشتن تنظیمات:

```bash
sudo ./scripts/uninstall.sh
```

حذف کامل همراه تنظیمات و secretها:

```bash
sudo ./scripts/uninstall.sh --purge
```

---

## محدودیت‌های نسخه فعلی

- فقط Linux پشتیبانی می‌شود.
- لایه ۳ به `/dev/net/tun` و `CAP_NET_ADMIN` نیاز دارد.
- ICMP در فیلتر لایه ۳ نسخه فعلی عبور داده نمی‌شود.
- حالت `both` ارسال هم‌زمان duplicate روی هر دو carrier نیست؛ مسیر ترجیحی را استفاده می‌کند و هنگام خطای قابل تشخیص به مسیر دوم می‌رود.
- failover نمی‌تواند packetهایی را که قبل از تشخیص خرابی در مسیر شبکه گم شده‌اند به‌طور کامل بازیابی کند.
- سیستم firewall، NAT و DNS را به‌صورت خودکار تغییر نمی‌دهد.
- قبل از production باید throughput، packet loss، MTU، reboot و failover روی شبکه واقعی آزمایش شوند.
