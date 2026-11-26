# HANDOFF — ghn-tele v2.16.0

Tài liệu bàn giao để tiếp tục phát triển ở nơi khác (Claude Code, VS Code...).

## 1. Mục tiêu

EXE Windows theo dõi 2 tab của GHN KTC – Vận tải (`Sắp đến kho`, `Đã đến kho`) và nhắn Telegram:
mỗi chuyến báo **lần đầu khi xuất hiện** (kể cả 0 kiện) → **mỗi lần số kiện đổi** → (⏰ nếu trễ) → **một lần khi vào kho** rồi dừng.
Đầu mỗi ca gửi tóm tắt, hết khung giờ gửi 📊 tổng kết. Dùng cho **một kho**. Theo dõi kho chạy độc lập. Kênh cập nhật dùng hai tệp tĩnh trên HTTPS.

## 2. Stack & ràng buộc

- Go 1.22+, **chỉ stdlib** (không dependency) → build 1 file EXE ~7.3 MB.
- Mọi dữ liệu nằm cạnh `.exe`: `config.json`, `seen.json`, `lastscan.json`, `watch.lock`, `ghn-tele.log`.
- UI = menu console tiếng Việt; chạy nền bằng tiến trình tách rời (Windows: `wscript` ẩn).

## 3. Cấu trúc

| File | Việc |
|---|---|
| `main.go` | menu, config, gọi API GHN, gửi Telegram, cài nhanh (`wizard` → `wizardSession/Token/Chats` → `autoStart`) |
| `watch.go` | vòng quét, so mã chuyến/số kiện, tin nhắn, lock, tiến trình nền, ca, task Windows |
| `session_browser.go` | đọc `SESSION` từ localStorage của Chrome/Edge/Cốc Cốc + kiểm tra với GHN |
| `leveldb.go`, `snappy.go` | đọc LevelDB (`.log` + `.ldb`/SSTable) và giải nén Snappy |
| `pin.go` | PIN dừng/sửa (`requirePIN`, khoá sau 3 lần sai, `pinlock.json`) |
| `guard.go` | tự bật lại: `running.flag`, `stopped.flag`, `--ensure`, tác vụ `GHN-DenKho-Watch` + `GHN-DenKho-Guard` |
| `bot.go` | lệnh Telegram: `startBot` (long-poll getUpdates trong tiến trình theo dõi), `runCommand`, `/xe` dùng `setSnapshot` của lần quét gần nhất, `botstate.json` (offset + chat đã thấy) |
| `handover.go` | bàn giao: `trackHandover` (seen.inKho, ⏳ kẹt quá `handoverMin`), `handoverSummary` cho 📊 |
| `doctor.go` | `--doctor`: in trạng thái thật (config, session, tiến trình, khung giờ, task, log) |
| `update*.go` | Kênh phát hành có chữ ký, menu 12, cập nhật từ chat riêng của chủ app, thay và khôi phục EXE, lịch cài tự động ngoài khung giờ quét |
| `demo.go` | `--demo`: in đúng các mẫu tin |
| `proc_windows.go`, `proc_unix.go` | `pidAlive`, `procStartTime`, `detach` |
| `*_test.go` | test logic thuần (không cần mạng) |

## 4. Luồng

```
watch()
   ├─ canResume(seen, lastscan ≤24h)? → silent=false (báo bù) : silent=true (chỉ ghi nhớ)
   ├─ summaryDue()? → 📊 summaryMsg(seen.Day), xoá Day
   ├─ trong khung → safePoll → pollLive() → msgs → tin mở đầu (👀/▶️) + msgs + 🕐 ca
   └─ ngủ nextSleep(): fastMin nếu có xe ETA trong [-60', +30'], còn lại liveMin
pollLive()
   ├─ ensureSession(): JWT < 4h → /iam/v1/session/extended; lỗi → lấy từ Chrome
   ├─ fetchArrived(), fetchUpcoming()
   ├─ diffScan()      (hàm thuần) → Arrived 🟠 · NewTrips 🔵 · Changes 🔄 · Late ⏰; cộng seen.Day
   ├─ diffMessages()  → gộp 🔵/🔄/⏰ khi >1 chuyến, 🟠 khi ≥4
   └─ cleanSeen(), saveSeen(), markScan() → trả msgs (watch() gửi qua hàng đợi)
lỗi ⇒ alert(key) tối đa 1 lần/giờ; hết lỗi ⇒ "✅ Đã kết nối lại"
sendLive() -> hàng đợi -> sendTelegram() -> mỗi target (nhóm/riêng)
```

## 5. API GHN đang dùng

Base `https://inside-prd-api.ghn.vn`; header `Authorization: Bearer <SESSION>`, `Origin: https://nhanh.ghn.vn`, `Referer` theo tab.

| Endpoint | Body chính |
|---|---|
| `POST /tms/v1/transportation/get-arrived-transports` | `offset, limit, locationId, locationType=GHN_HUB, stopSessionStatus=""` |
| `POST /tms/v1/transportation/get-upcoming-transports` | như trên + `startTime, endTime` (RFC3339, hôm nay) |
| `GET /iam/v1/session/extended` | gia hạn SESSION, trả về chuỗi mới ở `message` |
| `GET /tms/v1/transportation-transporting-list` | tab **Đã rời kho**; query `getTotal,offset,limit,locationId,locationType=GHN_HUB,source` (newRequestInside tự gắn); `data[]` phẳng, có `arrivalTime`, `departureTime` (chuỗi hoặc số ms → `flexTime`). Chỉ gọi khi `findVanished` trả chuyến |
| `POST /tms/v1/transportation/get-vehicle-location` | `{vehiclePlate}` → `data[0].latitude/longitude` (chỉ gọi khi báo ⏰) |

Nguồn đối chiếu (công khai, không cần đăng nhập): `https://nhanh.ghn.vn/ktc-van-tai/static/pages/transport/{initialization,arrival}/*.js`.
Các đường dẫn `transportation-*-list` trong `app.js` là khai báo quyền, 2 tab **không** gọi chúng.

Trường dùng: `partnerTransportationCode`, `routeScheduleName`, `typeOps`, `vehiclePlate`, `capacity`,
`driverName`, `driverPhone`, `arrivalTime`, `expectedArrivalTime`, `stopSessionStatus`,
`willDropOff{numOfPkg,numOfPkgBulky,numOfPkgFwd,numOfPkgFragile}` (dễ vỡ đếm riêng, không cộng tổng), `includeSortGroupCodes`, `excludeSortGroupCodes`.

## 6. Quy ước quan trọng (đừng phá)

1. **Escape HTML** mọi giá trị động (`esc`) — Telegram `parse_mode=HTML` trả 400 và mất cả tin nếu có `<`, `&`.
2. **Cắt tin > 3900 ký tự** (`splitMsg`) — giới hạn Telegram 4096.
3. **Dedup bằng mã chuyến**: `seen.json.sig` = chữ ký số kiện; có sig mà chưa có `arrived` = đang theo dõi. `sigAt` = lần cuối thấy ở Sắp đến (dọn sau 3 ngày).
4. **Không báo lại** chuyến đã có trong `arrived` (xoá sig khi vào kho; bỏ qua cả khi nó còn nằm ở Sắp đến).
8. **Lần gốc** (`silent`) chỉ khi không tiếp tục được (seen rỗng / lastscan > 24h), và chỉ hạ cờ khi quét **thành công cả 2 tab**.
10. **Mọi tin trong vòng quét đi qua `sendLive`** (hàng đợi) để đúng thứ tự; riêng 👀 gửi thẳng.
12. **Gửi tin**: `enqueue(loud|quiet)` → `deliver()` thử lại **chỉ nơi lỗi**; lỗi 400/403 = `tgPermanent`, không thử lại.
17. **Lệnh bot**: chủ app = `from.id == telegramPersonal`; chỉ trả lời nhóm đã cài + chat riêng chủ app; không cho đổi SESSION/token/kho/PIN; bỏ tin > 3 phút và backlog lúc mới bật.
16. **Dừng/tự bật lại**: dừng chỉ qua `cmdStop` (PIN → `setStopIntent` → kill). `--ensure` không bật khi có `stopped.flag`. Bật bằng tay qua `startHiddenWatch` (xoá `stopped.flag`). Không giấu tiến trình / chặn Task Manager.
15. **Bố cục tin**: dùng chung `tripHead` / `etaBlock` / `driverLine` / `cargoLine` / `listItem` — mỗi ý một dòng có nhãn; sửa bố cục ở đây, đừng dựng lại trong từng tin.
14. **Chống sót**: `seen.last` (bản cuối ở Sắp đến) + `seen.missing` (đếm lần vắng); `findVanished` cần `vanishAfter`=2 lần, chỉ xét ETA hôm nay, chỉ chạy khi tab Sắp đến lấy đủ.
13. **LevelDB**: khoá `.ldb` có 8 byte đuôi (seq<<8|loại) — luôn qua `parseInternalKey` + `mergeEntry` (seq lớn nhất thắng).
11. `seen.json` có `late` (đã báo trễ) và `day` (số liệu 📊) — giữ tương thích ngược khi thêm khoá.
9. **Cảnh báo lỗi** luôn qua `alert(cfg, key, text)`; lỗi mạng qua `netFailed` (báo sau `netAlertAfter`=3 lần liên tiếp), `netRecovered` khi quét OK — không gọi `sendTelegram` trực tiếp trong vòng quét.
5. **Múi giờ**: mọi giờ tính theo giờ máy (Việt Nam). `_ "time/tzdata"` đã nhúng.
6. **Chỉ một tiến trình**: `watch.lock` (pid + thời điểm + `start` = giờ tạo tiến trình). Mọi kiểm tra/dừng đi qua `lockAlive()` — **không** `Kill` một PID chỉ dựa vào `pidAlive`.
7. **Chạy nền**: Windows dùng `wscript run-watch.vbs` (ẩn, tách console); Unix `detach` (setsid).

## 7. Build & test

```bash
./build.sh            # EXE Windows -> dist/ghn-tele-<ver>.exe
./build.sh local      # bản chạy máy hiện tại
TZ=Asia/Ho_Chi_Minh go test -race ./...   # GHN/Telegram giả, cài nhanh và tệp LevelDB
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" .
```

## Phát hành qua GitHub

Push tag `vX.Y.Z` trùng `version` trong `main.go`. Workflow `.github/workflows/release.yml` dùng secret `GHN_TELE_RELEASE_KEY` để ký `update.json` và tạo Releases. Hướng dẫn cấu hình máy phát hành và máy kho ở `HUONG-DAN-GITHUB.md`.

## 8. Việc nên làm tiếp

- ~~Gộp tin 🔄~~, ~~"còn bao lâu" trong 🔵~~ — xong ở v2.3.0.
- ~~`watch.lock` bị PID tái dùng~~ — xong ở v2.3.1.
- Dùng cho một kho → không làm đa kho.
- Nếu GHN đổi API: chỉ sửa `fetchTransports` + struct `Transport`.
