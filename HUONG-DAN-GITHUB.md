# Cập nhật GHN Tele qua GitHub

Từ v2.16.0, máy kho có thể tự cài bản mới từ GitHub Releases. GitHub build EXE Windows 64-bit, ký thông tin bản phát hành và đăng cả hai tệp khi anh chạy workflow trên GitHub hoặc đẩy tag phiên bản.

## Thiết lập một lần

1. Workflow phát hành đã được cài trong repo `vietnhat111/GHN-Tele`. Máy kho tải từ GitHub Releases mà không cần tài khoản GitHub.
2. Trên máy phát hành, mở **Command Prompt** tại thư mục chứa EXE. Dùng lệnh sau để tạo khóa và cấu hình cho repo này. Nếu tệp EXE có tên khác, thay `ghn-tele.exe` bằng đúng tên tệp.

   ```cmd
   ghn-tele.exe --update-github-init release-private.key vietnhat111/GHN-Tele > update-channel.json
   ```

   Nếu dùng macOS, đứng trong thư mục mã nguồn và chạy `./build.sh local` sau khi cài Go. Dùng đường dẫn binary in ra sau khi build thay cho `ghn-tele.exe` trong lệnh trên.

3. Trên GitHub, vào **Settings → Secrets and variables → Actions → New repository secret**. Đặt tên secret là `GHN_TELE_RELEASE_KEY` và dán toàn bộ nội dung của `release-private.key`, gồm cả dấu ngoặc kép. Giữ bản sao khóa này trên máy phát hành. Không đưa khóa, SESSION GHN, token Telegram hay `config.json` vào repo.
4. Trên máy kho, chọn **mục 5** để dừng theo dõi, đóng app và chép EXE v2.16.0 đè đúng đường dẫn và tên EXE cũ. Giữ các tệp dữ liệu hiện có. Chép `update-channel.json` vừa tạo cạnh EXE, mở app và bật lại theo dõi. Nếu máy đang chạy theo dõi bằng tài khoản SYSTEM, dùng **mục 6** để cài lại tự chạy bằng tài khoản Windows vận hành app.
5. Nếu muốn nhận kết quả cài qua Telegram, thêm chat riêng của anh tại **mục 4**. Trong **mục 12**, app hiển thị chế độ tự cập nhật đang bật. Anh có thể tắt hoặc bật lại bằng PIN.

File `update-channel.json` tạo theo cách này đã bật tự cập nhật. Với tệp kênh cập nhật có sẵn từ v2.15.0, anh có thể giữ nguyên khóa và bật chế độ này qua mục 12. Nếu chuyển sang GitHub, sửa `manifestUrl` thành `https://github.com/vietnhat111/GHN-Tele/releases/latest/download/update.json` và dùng cặp khóa cũ để ký bản phát hành.

Nếu đã tạo `update-channel.json` khi repo còn tên `GHN`, chỉ sửa `manifestUrl` sang URL trên và giữ nguyên `publicKey` cùng khóa ký hiện có.

## Phát hành mỗi lần có bản mới

1. Khi mã nguồn mới đã lên nhánh `main`, mở workflow để phát hành. Mỗi bản mới có số `version` cao hơn trong `main.go`.
2. Trên GitHub, mở **Actions → Phát hành GHN Tele → Run workflow**. Giữ nhánh `main` rồi bấm **Run workflow**. Workflow tự đọc phiên bản từ mã nguồn đang có trên nhánh đó.
3. Khi job báo thành công, mở **Releases**. Anh sẽ thấy EXE `ghn-tele.exe`, ZIP mã nguồn `GHN-Tele-Source-<phiên-bản>.zip` và tệp `update.json` có chữ ký.

Anh cũng có thể phát hành bằng cách đẩy tag `vX.Y.Z` trùng với `version` trong `main.go`.

## Máy kho cài lúc nào?

- Trong lúc tiến trình theo dõi đang chạy và ngoài khung giờ đã đặt, app kiểm tra bản mới khi vừa khởi động và mỗi 15 phút. App cần còn ít nhất 10 phút trước giờ quét tiếp theo để bắt đầu cài tự động.
- App kiểm tra chữ ký và mã băm trước khi chạy EXE mới. Khi cài xong, app thử bật lại theo dõi. Khi EXE mới không qua bước kiểm tra khởi động sau khi thay, app thử phục hồi EXE trước và giữ kết quả trong `update-result.txt`.
- Nếu một phiên bản không qua kiểm tra khởi động, app sẽ chờ bản có số phiên bản cao hơn. Nếu Windows đang giữ tệp EXE và chưa thay được, app có thể thử lại sau một giờ. Anh vẫn có thể chủ động kiểm tra và cài bằng **mục 12**, hoặc mở chat riêng với bot, chọn **Menu → /menu → Kiểm tra bản mới → Cài v…**. Tính năng nút bấm có từ v2.16.1. Trên v2.16.0, dùng `/kiemtracapnhat` rồi `/capnhat <phiên-bản>`.
- Với máy theo dõi cả ngày, dùng mục 12 hoặc Telegram để chọn lúc cài. Máy kho phải mở, có mạng và chạy tiến trình theo dõi dưới tài khoản Windows đã đăng nhập để tự cập nhật.
