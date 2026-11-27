# Điều khiển GHN Tele từ điện thoại

## Mở bảng điều khiển

Trên máy kho đã chạy GHN Tele từ v2.16.5, cấu hình chat riêng của chủ app tại mục 4. Trong Telegram, mở chat riêng với bot bằng đúng tài khoản đó và gửi **/menu** hoặc **/dieukhien**.

| Nút | Thao tác |
| --- | --- |
| **Trạng thái** | Xem phiên bản, lần quét, khung giờ và tình trạng phiên GHN. |
| **Kiểm tra máy kho** | Xem lần quét đầy đủ, lỗi gần nhất, hàng đợi và lần gửi Telegram thành công của từng nơi nhận. Có nút **Gửi tin thử**. |
| **Quét ngay** | Yêu cầu một lượt quét trong khung giờ khi theo dõi đang bật. |
| **Cài đặt theo dõi** | Đổi giờ, ca, chu kỳ, quét nhanh, ngưỡng báo trễ, bàn giao, theo dõi xe sắp đến, nơi nhận tin, tự lấy phiên GHN và tên người quản lý. |
| **AI và tra cứu** | Đổi giọng, quyền trò chuyện và tra cứu trong nhóm, thời gian lưu lịch sử. Khóa dịch vụ được cài trên máy kho theo [hướng dẫn AI](HUONG-DAN-AI.md). |
| **Kiểm tra bản mới** | Xem bản trên kênh cập nhật và chọn cài nếu có bản cao hơn. |
| **Tự cập nhật** | Xem và bật/tắt chế độ tự cài ngoài giờ quét. |
| **Kết quả cập nhật** | Xem kết quả cài gần nhất. |
| **Tạm dừng / Báo lại** | Dùng lệnh `/tat PIN` trong chat riêng hoặc bấm **Báo lại**. |

## Ví dụ đổi giờ theo dõi

1. Bấm **Cài đặt theo dõi → Giờ theo dõi**.
2. Trả lời tin bot vừa gửi bằng `06:00 22:00`.
3. Kiểm tra dòng **Đang dùng** và **Sẽ đổi thành**. Bấm **Lưu** hoặc **Hủy**.
4. Khi bot xác nhận đã lưu, chu kỳ theo dõi sau sẽ đọc cấu hình mới.

Mỗi phiên nhập có hiệu lực 5 phút. Nếu giá trị đã được đổi chỗ khác trong lúc anh nhập, bot yêu cầu mở lại để xem giá trị mới. Với **Nơi nhận báo xe**, anh chọn nhóm, riêng hoặc cả hai trong số các chat đã cấu hình.

Máy kho cần đang chạy và có mạng để nhận lệnh. Cài đặt ban đầu cho bot, đổi tài khoản chủ app hoặc nhóm đích, đổi mã kho, nhập SESSION, đổi PIN, nhập khóa AI và tự khởi động Windows do người vận hành thực hiện trên máy kho.

## Trợ lý AI và kiểm tra máy kho từ 2.17.0

- Trong chat riêng với bot, mở `/menu` → **Trợ lý AI**. Anh có thể chọn **Gọn việc** hoặc **Em út vui vẻ**, bấm **Thử giọng bot** và bật hoặc tắt trợ lý.
- Khi trợ lý đã bật, anh hỏi thẳng trong chat riêng. Trong nhóm, tag bot hoặc reply tin bot. Ví dụ: “Có mấy chuyến trễ rồi em?”, “Xe nào tới đầu tiên?”, hoặc reply tin có một mã chuyến rồi hỏi “Còn mấy kiện?”.
- Câu hỏi xe dùng dữ liệu tại máy kho. Trò chuyện và soạn tin nhờ dịch vụ AI đã cấu hình. Khi nhờ soạn tin, bot gửi bản nháp trong cuộc chat đang dùng để anh đọc và tự quyết định có chuyển tiếp hay không.
- Vào **Kiểm tra máy kho** để xem số xe, số trang đã đọc, chuyến đang chờ đối soát và kết quả gửi từng nơi. App báo đổi giờ khi một lần quét nhận thấy thay đổi từ 15 phút hoặc khác ngày.
- Giữ `telegram-outbox.json` cạnh EXE khi thay bản. Từ phiên bản này, tệp lưu cả trạng thái theo dõi chuyến và tin chưa gửi. App đọc `seen.json` cũ khi chưa có trạng thái mới.

## Tra cứu chuyến từ 2.18.0

- Trong nhóm kho, dùng `/chuyen E123456`, `/chuyen 51C-12345` hoặc `/chuyen Đà Nẵng`. Nếu có nhiều kết quả, người hỏi bấm chọn một chuyến trong các gợi ý.
- Nếu bật AI nhóm, anh em có thể tag bot để hỏi “Tuyến Đà Nẵng đổi giờ lúc nào em?” hoặc reply tin báo “Xe này bàn giao xong chưa?”. Khi tin có nhiều chuyến, bot đưa nút chọn và vẫn giữ câu hỏi ban đầu.
- Chat riêng của chủ app có `/menu` → **AI và tra cứu**. Có thể bật/tắt riêng **AI nhóm** và **Tra cứu nhóm**, chọn giọng và chọn giữ lịch sử 3, 7 hoặc 14 ngày (mặc định 7 ngày). Lịch sử bắt đầu từ khi cài bản mới và quét GHN, có giới hạn 1.500 chuyến gần nhất.
- Các lệnh và nút quản trị, kể cả cập nhật, dùng trong chat riêng của chủ app. Menu lệnh và `/lenh` trong nhóm chỉ hiện lệnh dành cho thành viên. Nút cài đặt từ tin cũ trong nhóm sẽ bị từ chối.

## Bàn giao ca và hỏi xe từ 2.19.0

- Khi AI nhóm đã bật, anh em có thể tag bot và hỏi “Ca mình còn việc gì em?”. Bot trả tóm tắt xe sắp tới, xe quá giờ dự kiến và xe chưa bàn giao xong, kèm thời điểm quét GHN.
- Quản lý mở chat riêng, gõ `/menu` → **Soạn bàn giao ca** để lấy bản nháp. Anh chọn gửi hoặc chuyển tiếp sau khi đọc.
- Reply tin báo để hỏi “Xe này bốc hàng xong chưa?”. Nếu chưa rõ chuyến nào, bot hỏi lại hoặc đưa nút chọn. Câu “Vậy xe nào vô trước?” trả về chuyến kế tiếp của kho.
- Số kiện bot đọc là số GHN ghi cần xuống kho. Bot chỉ kết luận từ dữ liệu GHN và báo rõ nếu lần quét mới nhất chưa đầy đủ.
