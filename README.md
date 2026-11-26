# GHN Đã đến kho → Telegram (`ghn-tele`)

EXE nhỏ cho máy kho: quét **Sắp đến kho** / **Đã đến kho** trên `nhanh.ghn.vn` và nhắn Telegram.
Vòng đời mỗi chuyến, lặp lại theo ca:

1. **🔵 Lần 1 — xe xuất hiện** ở tab Sắp đến, kể cả **chưa có kiện** (0 kiện).
2. **🔄 Mỗi lần số kiện thay đổi** — bao rã / cồng kềnh / chuyển tiếp / dễ vỡ, kèm chênh lệch.
3. **🟠 Xe đã đến kho** — báo một lần rồi **dừng hẳn** với chuyến đó.
4. **🕐 Đầu ca** — tóm tắt xe đang sắp đến + trong kho; sang ca mới chu kỳ lặp lại (tắt được).
5. **⏰ Xe trễ** quá 30 phút mà chưa vào kho — báo một lần, kèm SĐT tài xế.
6. **📊 Tổng kết** khi hết khung giờ — số xe, số kiện, xe trễ; cũng là tín hiệu app còn sống.

---

## 1. Cài (2 phút)

1. Chép `ghn-tele.exe` vào `C:\GHN-Tele\` (không dấu, không cách).
2. Mở **Chrome** trên máy đó, đăng nhập `nhanh.ghn.vn`, vào trang KTC – Vận tải.
3. Tạo bot: @BotFather → `/newbot` → copy token. Thêm bot vào nhóm, gõ `/start` trong nhóm
   (muốn nhận cả chat riêng: mở bot, bấm Start).
4. Chạy exe → chọn **1** (cài nhanh):
   - **Bước 1** — app tự lấy SESSION + mã kho từ Chrome/Edge/Cốc Cốc (không cần F12).
   - **Bước 2** — dán Bot Token, app kiểm tra ngay.
   - **Bước 3** — Enter: app tự tìm nhóm (và chat riêng), nhiều nhóm thì cho chọn.
   - App gửi tin thử → hỏi **"Bật theo dõi ngay + tự chạy khi đăng nhập Windows?"** → Enter. Xong.

Kiểm tra: **mục 9** — `Theo dõi: ĐANG CHẠY` + `Lần quét cuối`. Cài lại từ đầu: mục 1 → `2`, hoặc `ghn-tele.exe --setup`.

---

## 2. Menu

| # | Việc |
|---|---|
| 1 | Cài đặt nhanh / sửa cấu hình |
| 2 | Gửi tin thử Telegram |
| 3 | Test lấy dữ liệu GHN |
| 4 | Chọn nhóm / chat riêng / cả hai |
| 5 | Chạy / dừng theo dõi |
| 6 | Tự chạy + tự bật lại khi bị tắt (cài / gỡ) |
| 7 | Lấy SESSION từ Chrome |
| 8 | Khung giờ theo dõi + danh sách ca |
| 9 | Xem trạng thái |
| 10 | Demo tin nhắn Telegram |
| 11 | Tạo phím tắt dừng Ctrl+Alt+S |
| 12 | Kiểm tra và cài bản cập nhật |

Dòng lệnh: `--setup`, `--watch`, `--stop`, `--run-once`, `--session`, `--doctor`, `--debug-session`,
`--install-watch`, `--remove-watch`, `--demo`, `--check-update`, `--update`, `--version`, `--help`.

### Lệnh Telegram

Gõ `/` trong nhóm là hiện danh sách. Bot chỉ trả lời trong **nhóm đã cài** và **chat riêng của chủ app**; trả lời im lặng (không rung máy).

| Lệnh | Việc | Ai dùng |
|---|---|---|
| `/gioithieu` | Giới thiệu bot: báo gì, khi nào, ý nghĩa ký hiệu (nên ghim trong nhóm) | Mọi người |
| `/xe` | Xe đang trong kho + xe sắp đến (số liệu lần quét gần nhất) | Mọi người |
| `/app` | App có chạy không, lần quét cuối, SESSION còn bao lâu, cài đặt | Mọi người |
| `/lenh` | Danh sách lệnh | Mọi người |
| `/menu` | Mở nút **Trạng thái app**, **Kiểm tra bản mới** và **Cài phiên bản** khi có bản mới | Chat riêng của chủ app |
| `/kiemtracapnhat` | Xem phiên bản mới và ghi chú phát hành | Chat riêng của chủ app |
| `/capnhat 2.15.1` | Cài đúng phiên bản đã chọn, nhận kết quả qua bot | Chat riêng của chủ app |
| `/tat 1234` | Tạm dừng báo (kèm PIN; tin chứa PIN tự xoá) | Chủ app |
| `/bat` | Báo lại, bù thay đổi lúc dừng | Chủ app |
| `/gio 05:00 21:00` | Khung giờ theo dõi | Chủ app |
| `/ca tat` · `/ca 05:00-08:00,…` | Tắt / đổi tin đầu ca | Chủ app |
| `/tre 30` | Báo xe trễ quá N phút (0 = tắt) | Chủ app |
| `/quet 5` | Chu kỳ quét 2–60 phút | Chủ app |
| `/bangiao 90` | Báo xe trong kho quá N phút chưa bàn giao xong (0 = tắt) | Chủ app |
| `/quanly Lê Nhật` | Tên người quản lý (hiện trong `/gioithieu`, `/app`, tin ⚠️; bấm vào mở Telegram của chủ app) | Chủ app |

**Chủ app** = tài khoản Telegram của chat riêng đã cài (mục 4 / cài nhanh). Mỗi lần đổi cài đặt, nhóm nhận tin `⚙️ … (bởi tên)`.
Không đổi được qua bot: SESSION, Bot Token, mã kho, PIN. Lệnh không gọi thêm GHN.

---

## Tự cập nhật qua GitHub từ v2.16.0

Xem [hướng dẫn cài và phát hành qua GitHub](HUONG-DAN-GITHUB.md). Khi bật tính năng ở mục 12, máy kho tự kiểm tra và cài bản mới ngoài khung giờ quét theo cấu hình.

## Cập nhật thủ công từ kênh HTTPS

### Trên máy kho

1. Nếu nâng cấp bằng tay, dừng theo dõi bằng mục 5, đóng các cửa sổ GHN Tele, rồi chép EXE mới đè đúng **đường dẫn và tên EXE cũ**. Giữ các tệp cấu hình và dữ liệu bên cạnh EXE. Mở lại và bật theo dõi bằng mục 5 nếu trước đó đang bật.
2. Chép `update-channel.json` do người phát hành tạo cạnh EXE. Tệp này chứa URL HTTPS của kênh và khóa công khai. Để gửi lệnh từ xa, cài chat riêng của chủ app ở mục 4 và giữ tiến trình theo dõi chạy bằng tài khoản Windows đã đăng nhập. Nếu trước đó app tự chạy dưới SYSTEM, dừng tiến trình cũ rồi dùng mục 6 để cài lại tự chạy bằng tài khoản vận hành. Khi có kênh cập nhật, tác vụ tự chạy và tự bật lại yêu cầu tài khoản này đã đăng nhập. Kiểm tra mục 9 sau khi cài lại tác vụ.
3. Tại máy kho, chọn mục **12**, chọn **1** để kiểm tra bản mới và nhập PIN khi xác nhận cài. Từ Telegram, nhắn riêng `/kiemtracapnhat` rồi dùng phiên bản bot trả về, ví dụ `/capnhat 2.16.1`. Máy kho cần tải được cả hai URL HTTPS.

App kiểm tra chữ ký Ed25519, phiên bản, hệ điều hành, kiến trúc, kích thước và SHA-256 trước khi chạy EXE mới. Tiến trình theo dõi chờ vòng quét và tin đã xếp hàng trước khi nhường EXE, sau đó app thử bật lại theo dõi nếu nó đang chạy trước khi cài. Khi kiểm tra khởi động sau khi thay EXE thất bại, app thử phục hồi bản trước. Xem kết quả trong chat riêng, mục 12 hoặc `update-result.txt`.

Bản sao gần nhất là `<tên-exe>.previous`. Để phục hồi thủ công, dừng theo dõi, đóng các cửa sổ GHN Tele rồi chép tệp sao lưu đè lên EXE với tên cũ. Chỉ người vận hành và quản trị viên nên có quyền ghi thư mục app và tệp kênh cập nhật.

### Phát hành bản mới

Dùng EXE hoặc binary GHN Tele v2.15.0 trở lên để tạo khóa và ký. Trên Windows chạy ví dụ trong Command Prompt. Trên macOS/Linux có thể build binary phát hành bằng `bash build.sh local` và thay lệnh `ghn-tele.exe` bằng đường dẫn tới binary đó.

Tạo kênh một lần. Thay `updates.example.com` bằng máy chủ HTTPS do anh quản lý.

```text
ghn-tele.exe --update-keygen release-private.key https://updates.example.com/ghn-tele/update.json > update-channel.json
```

Giữ `release-private.key` trên máy phát hành và tạo bản sao lưu riêng. Chỉ chuyển `update-channel.json` sang máy kho.

Mỗi lần phát hành, tăng `version` trong `main.go`, build, ký đúng EXE đã build và dùng lại khóa cũ.

```text
ghn-tele.exe --update-sign release-private.key 2.16.1 windows amd64 dist/ghn-tele-2.16.1.exe https://updates.example.com/ghn-tele/ghn-tele-2.16.1.exe "Ghi chu ban moi" > update.json
```

Đăng EXE lên URL tải xuống trước, sau đó đăng `update.json` lên URL của kênh. Khi thử lần đầu, dùng `--check-update` trên máy kho để xác nhận app thấy đúng số phiên bản. Hạn chế cache cho `update.json` để máy kho thấy bản vừa đăng. Mỗi tệp kênh trỏ đến bản phát hành cho một hệ điều hành và kiến trúc.

---

## 3. Cách app quyết định gửi tin

Mỗi vòng quét (mặc định 5 phút), chỉ trong khung giờ đã đặt:

| Tình huống | Gửi | Ghi nhớ |
|---|---|---|
| Chuyến **mới xuất hiện** ở Sắp đến (kể cả 0 kiện) | 🔵 lần 1 | chữ ký số kiện |
| **Số kiện đổi** so với lần trước | 🔄 kèm diff | cập nhật chữ ký |
| Quá giờ dự kiến > `lateMin` mà chưa vào kho | ⏰ một lần | đánh dấu đã báo trễ |
| Chuyến xuất hiện ở **Đã đến** | 🟠 rồi **dừng** | xoá chữ ký, đánh dấu đã đến |
| Chuyến **biến khỏi Sắp đến** 2 lần quét liền, không qua Đã đến | tra **Đã rời kho**: thấy → 🟠 (kèm giờ đến/rời); không thấy → ❔ im lặng | đánh dấu đã xử lý |
| Vào **ca mới** | 🕐 tóm tắt đầu ca | tên ca hiện tại |
| **Hết khung giờ** | 📊 tổng kết | xoá số liệu ngày |

- **Nhiều xe xuất hiện cùng lúc** → gộp một tin `🔵 3 XE MỚI SẮP ĐẾN` (một xe thì gửi tin chi tiết).
- **Nhiều xe đổi kiện cùng lúc** → gộp một tin `🔄 CẬP NHẬT SỐ KIỆN — 3 CHUYẾN`; xe trễ cũng gộp.
- **Từ 4 xe vào kho cùng vòng** (thường là sáng ra, bù xe vào trong đêm) → một tin `🟠 4 XE ĐÃ VÀO KHO` kèm tổng kiện. 1–3 xe vẫn báo chi tiết từng xe.
- **Quét thích ứng:** bình thường mỗi `liveMin` phút (5); có xe dự kiến tới trong 30 phút thì quét mỗi `fastMin` phút (2) để báo 🟠 sớm.
- Tin 🔵 kèm **thời gian còn lại** tới giờ dự kiến (`còn 40 phút` / `trễ 15 phút`).
- Không giới hạn số lần 🔄: đổi kiện lúc nào báo lúc đó, đến khi xe vào kho.
- `seen.json` tự quên chuyến quá 3 ngày (kể cả chuyến biến mất khỏi Sắp đến mà không vào kho).
- **Bật lại** khi lần quét trước cách đây ≤ 24 giờ → `▶️ TIẾP TỤC THEO DÕI` và **báo bù** mọi thay đổi trong lúc tắt (đã gộp, không bị dội).
- Chưa từng chạy / dữ liệu cũ hơn 24 giờ → lần quét **thành công** đầu chỉ gửi `👀 BẮT ĐẦU THEO DÕI`, không báo từng chuyến.

---

## 4. `config.json`

| Khoá | Ý nghĩa | Mặc định |
|---|---|---|
| `session` | SESSION GHN (trống = tự lấy từ Chrome) | — |
| `locationId` | Mã kho | — |
| `telegramToken` | Token bot | — |
| `telegramChat` | ID nhóm (số âm) hoặc `@kenh` | — |
| `telegramPersonalChat` | Chat ID riêng của anh | trống |
| `notifyMode` | `group` · `personal` · `both` | tự suy theo ID có sẵn |
| `telegramThread` | Topic của nhóm forum | `0` |
| `liveMin` | Quét mỗi N phút | `5` |
| `notifyUpcoming` | Lấy cả tab Sắp đến | `true` |
| `watchFrom` / `watchTo` | Khung giờ theo dõi | `05:00` / `21:00` |
| `shifts` | Danh sách ca (mỗi ca gửi tóm tắt đầu ca); `tắt` = không báo đầu ca | `05:00-08:00,11:00-14:00,18:00-21:00` |
| `lateMin` | Báo ⏰ khi xe trễ quá N phút (`0` = tắt) | `30` |
| `handoverMin` | Báo ⏳ khi xe trong kho quá N phút chưa bàn giao xong (`0` = tắt) | `90` |
| `fastMin` | Quét nhanh mỗi N phút khi có xe tới trong 30' (`0` = tắt) | `2` |
| `autoSession` | Tự lấy SESSION từ trình duyệt | `true` |
| `chromeDir` | Đường dẫn leveldb chỉ định | trống |

---

## 5. Vận hành

- **Chạy nền:** mục 5 khởi động tiến trình ẩn, tách khỏi cửa sổ. Bấm X không dừng app.
- **Dừng:** mục 5 (hỏi *Dừng?*) · `Ctrl+Alt+S` sau khi tạo ở mục 11 · `ghn-tele.exe --stop`. Cả ba đều gửi `⏹ ĐÃ DỪNG THEO DÕI` vào nhóm.
- **Chỉ một tiến trình:** `watch.lock` chặn mở 2 vòng quét (tránh tin trùng).
- **SESSION:** tự gia hạn khi còn < 4 giờ; GHN từ chối thì lấy lại từ Chrome; không được nữa thì nhắn cảnh báo — **tối đa 1 lần/giờ** cho mỗi loại lỗi, có tin `✅ Đã kết nối lại` khi hết lỗi.
- **Lỗi gửi Telegram** được ghi log và thử lại 1 lần.

---

## 6. Sự cố

| Hiện tượng | Xử lý |
|---|---|
| Không có tin nào | Mục 9 xem `Theo dõi` + `Lần quét cuối`; `ghn-tele.exe --doctor` |
| Báo "ngoài khung giờ" | Mục 8 đổi khung (cả ngày: `00:00`–`23:59`) |
| 401 / SESSION hết hạn | Mục 7 lấy lại; vẫn lỗi → `--debug-session` |
| `chat not found` | Mục 4 chọn lại nhóm; bot phải còn trong nhóm |
| Tin đến 2 lần | Đang chạy 2 tiến trình → mục 5 dừng bớt |
| Không thấy xe | Xe chưa có kiện, hoặc ngoài khung |

---

## 7. Build

```bash
./build.sh          # EXE Windows (dist/)
./build.sh local    # bản chạy trên máy hiện tại
TZ=Asia/Ho_Chi_Minh go test ./...       # test logic thuần
```

Cấu trúc: `main.go` (menu, cấu hình, API GHN, gửi Telegram) · `watch.go` (vòng quét, tin nhắn, tiến trình nền)
· `session_browser.go` + `leveldb.go` + `snappy.go` (tự lấy SESSION) · `doctor.go` · `demo.go` · `proc_*.go`.

---

## 8. Thay đổi

### v2.16.1

- Thêm `/menu` trong chat riêng của chủ app. Bấm **Kiểm tra bản mới** rồi bấm **Cài v…** nếu muốn cập nhật ngay.
- Nút cài chỉ áp dụng cho phiên bản hiển thị và có hiệu lực trong 15 phút.

### v2.16.0

- Thêm tự kiểm tra và cài bản mới ngoài khung giờ quét.
- Thêm quy trình GitHub Actions để build, ký và phát hành EXE Windows theo tag phiên bản.

### v2.15.0

- Thêm mục 12, lệnh Telegram kiểm tra và cài bản mới từ kênh phát hành có chữ ký.
- Thay EXE sau khi dừng quét an toàn, giữ một bản EXE dự phòng và báo kết quả cài.

### v2.14.3

- GHN từ chối SESSION (401/403) → app **tự lấy SESSION mới từ Chrome và thử lại ngay**; chỉ báo ⚠️ khi vẫn không được.
- `/app` luôn hiện dòng SESSION (còn bao lâu / không đọc được hạn / chưa có) và dòng lần quét cuối (kể cả "chưa quét lần nào").

### v2.14.2 — lỗi mạng không làm phiền

- Lỗi mạng / GHN không phản hồi: chỉ báo khi **3 lần quét liên tiếp** (≈ 15 phút) vẫn lỗi; lỗi chớp nhoáng tự hết thì im.
- Tin lỗi viết dễ hiểu (mất mạng / mạng chậm / máy chủ GHN lỗi), bỏ đường link kỹ thuật; khi khỏi báo `✅ Đã kết nối lại GHN sau N phút`.
- SESSION còn hạn mà gia hạn lỗi (thường do mạng) → dùng tiếp, không báo nhầm "SESSION hết hạn". Lỗi SESSION thật vẫn báo ngay.

### v2.14.1

- Ngưỡng mặc định báo xe kẹt bàn giao: **90 phút** (trước 60). Ai đã đặt bằng `/bangiao` thì giữ nguyên. Gợi ý: sau 1 tuần đặt ≈ gấp đôi "trung bình N phút/xe" trong 📊.

### v2.14.0 — theo dõi bàn giao

- **Mới:** ⏳ **xe kẹt bàn giao** — vào kho quá 90 phút (đổi bằng `/bangiao`, mục 1) mà chưa *Đã bàn giao* → báo 1 lần, có chuông, kèm SĐT tài xế.
- **Mới:** 📊 tổng kết thêm `✅ Bàn giao xong: X/Y xe · trung bình N phút/xe` và `⏳ Chưa bàn giao xong: <mã chuyến>`.
- Lấy từ trạng thái ở tab Đã đến kho (Chưa → Đang → Đã bàn giao); xe rời tab = bàn giao xong. Không gọi thêm GHN.

### v2.13.0 — người quản lý bot

- **Mới:** `/gioithieu`, `/app` và tin ⚠️ bị tắt ngang hiện **"👤 Người quản lý bot: Lê Nhật"** — bấm vào mở Telegram của chủ app (`t.me/username`, không có username thì theo mã tài khoản).
- Tên + @username tự lấy từ tài khoản Telegram của chủ app; đổi bằng `/quanly <tên>` hoặc mục 1.

### v2.12.0 — giới thiệu bot

- **Mới:** `/gioithieu` (và `/start` khi thêm bot vào nhóm) — giải thích bot làm gì, tin nào có chuông / im lặng, ký hiệu, lệnh; nội dung tự theo cài đặt (khung giờ, chu kỳ quét, ngưỡng trễ, ca).
- Hồ sơ bot (mô tả + mô tả ngắn) tự cập nhật khi app chạy.

### v2.11.0 — lệnh Telegram

- **Mới:** `/xe`, `/app`, `/lenh` cho cả nhóm; `/tat PIN`, `/bat`, `/gio`, `/ca`, `/tre`, `/quet` chỉ chủ app. Đổi cài đặt → báo nhóm `⚙️`.
- Bot bỏ qua lệnh tồn đọng lúc app vừa bật (và tin cũ hơn 3 phút); chat lạ gõ lệnh → im lặng.
- Có chương trình khác cùng dùng bot → cảnh báo (tối đa 1 lần/giờ).
- Mục 4 vẫn tìm được nhóm khi app đang chạy (bot ghi nhớ các chat đã nhắn).

### v2.10.0 — chặn người khác tắt app

- **Mới: mã PIN** (mặc định **1234**, đổi ở mục 1 → sửa từng mục) cho: dừng theo dõi (mục 5, `Ctrl+Alt+S`, `--stop`), sửa cấu hình (mục 1), đổi nơi nhận (mục 4), tự chạy (mục 6), khung giờ (mục 8), `--setup`, `--remove-watch`. Sai 3 lần → khoá 5 phút + báo nhóm `🔒`.
- **Mới: tự bật lại** — tác vụ `GHN-DenKho-Guard` trong Task Scheduler kiểm tra mỗi 5 phút; ai tắt app bằng Task Manager thì app tự lên lại và báo `⚠️ APP BỊ TẮT NGANG — ĐÃ TỰ BẬT LẠI` (có chuông). Máy khởi động lại → tin im lặng `ℹ️`.
- Dừng bằng PIN = dừng có chủ ý → **không** tự bật lại (kể cả khi đăng nhập Windows) cho tới khi bật ở mục 5.
- `Ctrl+Alt+S` giờ mở cửa sổ hỏi PIN (phím tắt cũ tự cập nhật, không cần tạo lại).
- Sửa: `--setup` trước đây chỉ có trong hướng dẫn, giờ chạy được thật.
- Không giấu tiến trình, không chặn Task Manager: `ghn-tele.exe` và 2 tác vụ vẫn hiện rõ.

### v2.9.0 — tin dễ đọc trên điện thoại

- Mỗi ý **một dòng, có nhãn đầy đủ**: Chuyến · Tuyến · Dự kiến đến · Còn bao lâu · Tài xế · SĐT · Xe · Cần nhận (từng loại kiện xuống dòng). Không còn dòng dài bị gãy giữa chừng.
- SĐT nằm riêng một dòng → Telegram cho bấm gọi. Tải trọng ghi `8.000 kg`.
- Tin 🟠 ghi rõ **Đến kho lúc**, **đến sớm/trễ so với dự kiến**, **trạng thái bàn giao**.
- Tin gộp nhiều xe: mỗi xe 2 dòng (mã + tuyến, rồi chi tiết có nhãn).

### v2.8.0 — chống sót xe vào kho

- **Mới:** xe vào rồi **rời kho giữa 2 lần quét** (không kịp thấy ở tab Đã đến) giờ vẫn được báo 🟠: chuyến đang theo dõi mà 2 lần quét liền không còn ở Sắp đến / Đã đến → app tra tab **Đã rời kho** (`GET transportation-transporting-list`, chỉ gọi khi có chuyến biến mất) và báo kèm **giờ đến + giờ rời kho**.
- **Mới:** không thấy ở đâu → tin im lặng ❔ "chuyến không còn ở Sắp đến" (huỷ / đổi kho / đổi lịch). Chuyến dự kiến ngày khác không bị tính.
- Bật lại sau khi tắt cũng bù được xe vào-ra trong lúc tắt; tổng kết 📊 đếm cả các chuyến này.

### v2.7.0 — tin báo đầy đủ hơn (đối chiếu mã trang GHN)

- Đã đối chiếu với mã JavaScript của chính trang GHN: 2 tab dùng đúng `POST get-upcoming-transports` / `get-arrived-transports` như app.
- **Mới:** tin ⏰ xe trễ kèm **link vị trí xe trên Google Maps** (API `get-vehicle-location` — nút bản đồ trên web); tối đa 5 xe/vòng.
- **Mới:** hiện **nhóm hàng** (`Thuộc nhóm` / `Ngoại trừ nhóm`) như cột "Nhóm hàng" trên web.
- **Mới:** kiện **dễ vỡ** là dòng cảnh báo riêng (GHN đếm riêng, không cộng vào tổng).
- Phân trang dùng `total` GHN trả về → không gọi thừa trang rỗng.

### v2.6.0 — rà lại toàn bộ: tự lấy SESSION chắc chắn hơn, tin rõ hơn

- **Sửa (quan trọng):** tự lấy SESSION **thất bại sau khi Chrome khởi động lại**. Chrome dồn dữ liệu từ `.log` sang `.ldb`; code cũ đọc sai khoá trong `.ldb` nên không thấy SESSION/mã kho. Giờ đọc đúng và luôn lấy bản mới nhất; đăng xuất thì không lấy lại SESSION cũ.
- **Sửa:** báo vào cả nhóm + chat riêng mà một nơi lỗi → nơi kia **nhận trùng tin**. Giờ chỉ gửi lại nơi lỗi.
- **Sửa:** mất mạng vài giây là **mất luôn tin 🟠**. Giờ thử lại tới ~3 phút; lỗi vĩnh viễn (chat not found) thì không thử.
- **Mới:** **tin im lặng** — 🔵 🟠 ⏰ ⚠️ đổ chuông; 🔄 🕐 📊 ▶️ ✅ vẫn hiện nhưng không rung máy.
- **Mới:** tin 🟠 ghi **đến sớm/trễ bao nhiêu** so với lịch; mọi tin ghi **chi tiết kiện** theo đúng 3 nhóm của web (cồng kềnh · cần rã · chuyển tiếp).
- **Sửa:** xe chưa gán tài xế/xe (GHN để "-") không còn in `👤 - — -   🚛 -`, thay bằng "Chưa gán tài xế / xe".
- Đọc được SESSION từ Opera; đổi khung giờ không cần bật lại theo dõi.

### v2.5.0 — cài gần như tự động

- **Mới:** cài nhanh 3 bước — app tự lấy **SESSION + mã kho** từ trình duyệt (bỏ bước F12), **kiểm tra Bot Token** ngay khi dán, **tự tìm nhóm / chat riêng** (một nhóm thì tự chọn), gửi tin thử, rồi một câu hỏi để **bật chạy nền + tự chạy khi đăng nhập Windows**.
- **Mới:** `--setup`; mục 1 khi đã cài cho chọn "sửa từng mục" hoặc "cài nhanh lại".
- **Sửa:** nhóm có Topic có thể bị lưu nhầm topic của tin **cũ nhất** thay vì mới nhất (mục 4).

### v2.4.0 — không sót tin, biết app còn sống

- **Mới:** bật lại (khởi động máy, cập nhật bản mới) trong vòng 24 giờ → **báo bù** những gì xảy ra lúc tắt, thay vì im lặng bỏ qua.
- **Mới:** từ 4 xe vào kho cùng lúc → gộp **một tin 🟠** (sáng ra không bị dội 15–20 tin).
- **Mới:** **📊 tổng kết** khi hết khung giờ (xe vào kho, số kiện, xe trễ, xe còn ở Sắp đến). Máy tắt trước giờ thì gửi khi bật lại. Hôm nào không thấy tin này = app đã chết.
- **Mới:** **⏰ báo xe trễ** quá `lateMin` phút (mặc định 30) mà chưa vào kho, một lần mỗi chuyến, kèm SĐT tài xế.
- **Mới:** **quét thích ứng** — có xe sắp tới trong 30' thì quét 2 phút/lần, còn lại 5 phút.
- **Mới:** tắt tin đầu ca bằng `shifts: "tắt"` (mục 8).
- Mục 1 (sửa cấu hình) hỏi thêm ngưỡng trễ + quét nhanh; mục 9 và `--doctor` hiện các thiết lập mới, và cho biết bật lại sẽ "tiếp tục" hay "bắt đầu mới".

### v2.3.1 — khoá tiến trình an toàn

- **Sửa (quan trọng):** Windows cấp lại PID cũ cho chương trình khác → menu báo nhầm `ĐANG CHẠY` (không bật được theo dõi) và **lệnh dừng có thể tắt nhầm chương trình đó**. `watch.lock` giờ lưu cả giờ tạo tiến trình; chỉ nhận/dừng khi khớp cả PID lẫn giờ tạo.
- **Sửa:** kiểm tra tiến trình trên Windows hỏi thẳng hệ điều hành thay vì đọc `tasklist` (trước đây PID `12` có thể khớp nhầm dòng chứa `1234`), nhanh hơn và không mở thêm tiến trình mỗi lần vẽ menu.
- Tiến trình thoát chỉ xoá `watch.lock` nếu lock vẫn là của nó.
- Tương thích: `watch.lock` của bản cũ vẫn đọc được.


### v2.3.0 — chắc hơn khi mạng / SESSION chập chờn

- **Sửa:** lần quét đầu bị lỗi (mất mạng, SESSION hết hạn lúc bật) làm lần sau coi mọi chuyến là mới → xả hàng loạt tin 🟠/🔵. Giờ chỉ chốt "gốc" khi quét thành công cả 2 tab.
- **Sửa:** cảnh báo SESSION/GHN lỗi bị nhắn lại mỗi vòng quét (5 phút/lần suốt đêm). Giờ tối đa 1 lần/giờ mỗi loại, báo `✅ Đã kết nối lại` khi khỏi.
- **Sửa:** quét lỗi đúng lúc sang ca → gửi tóm tắt đầu ca "0 xe". Giờ chờ vòng quét thành công.
- **Sửa:** chuyến đã báo 🟠 nhưng GHN còn để ở Sắp đến không bị báo 🔵 lại.
- **Sửa:** chữ ký chuyến huỷ / không bao giờ đến nằm mãi trong `seen.json` → giờ dọn sau 3 ngày.
- **Sửa:** tin thử (mục 2) báo "chưa có Chat ID" khi chỉ dùng chat riêng; số mục menu ghi sai trong hướng dẫn.
- **Mới:** gộp tin 🔄 khi nhiều chuyến đổi kiện cùng vòng; tin 🔵 hiện "còn bao lâu".
- Tóm tắt đầu ca đi qua cùng hàng đợi với các tin khác → đúng thứ tự.


### v2.2.0 — gộp tin + chọn nơi nhận

- **Mới:** nhiều xe mới trong cùng vòng quét → **một tin** `🔵 3 XE MỚI SẮP ĐẾN` (một xe vẫn gửi chi tiết).
- **Mới:** báo **nhóm / chat riêng / cả hai** (`notifyMode`); mỗi tin gửi tới mọi nơi đã chọn, nơi lỗi không chặn nơi kia.
- **Mới:** mục 4 nhận cả nhóm và chat riêng, hỏi chọn nơi nhận; mục 9 hiển thị rõ đang báo đi đâu.
- Xuất kèm **mã nguồn** + `HANDOFF.md` để làm tiếp ở nơi khác.


### v2.1.0 — đúng vòng đời anh cần

- **🔵 lần 1** báo ngay khi chuyến xuất hiện ở Sắp đến, **kể cả 0 kiện** (trước đây chờ có hàng / chờ gần giờ).
- **🔄 báo mỗi lần số kiện đổi** (không giới hạn), kèm chênh lệch từng loại.
- **🟠 xe vào kho là chốt**, xoá trạng thái chuyến, không báo lại.
- **🕐 đầu ca**: tóm tắt xe sắp đến + trong kho; sang ca mới lặp lại chu kỳ.
- Bỏ báo "quá giờ" và "báo trước N phút" (đã thay bằng lần 1 khi xuất hiện).

### v2.0.0 — gọn lại đúng mục đích

- **Bỏ** bot 2 chiều (`/homnay`, `/xe`, `/kho`), báo đổi tình trạng bàn giao, báo đổi số kiện, ca làm việc + tin đầu/kết ca, file mẫu tin `.txt`, `cargoRule`, `onlyHasPkg`, `upcomingDays`.
- **Giữ đúng 3 tin:** 🔔 sắp đến · ⏰ quá giờ · 🟠 đã đến. Mỗi chuyến một lần.
- Menu từ 21 mục còn **12 mục**; `config.json` từ 20 khoá còn **12**.
- Vòng quét chỉ gọi 2 API/lần (trước đây có lúc gọi 3 lần vì đếm lại danh sách).
- `seen.json` gọn (chỉ mã chuyến đã báo), tự dọn sau 3 ngày.
- Chạy nền ẩn: đóng cửa sổ không dừng; dừng có báo Telegram.
