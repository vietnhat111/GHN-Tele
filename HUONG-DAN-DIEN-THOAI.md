# Điều khiển GHN Tele từ điện thoại

## Mở bảng điều khiển

Trên máy kho đã chạy GHN Tele từ v2.16.5, cấu hình chat riêng của chủ app tại mục 4. Trong Telegram, mở chat riêng với bot bằng đúng tài khoản đó và gửi **/menu** hoặc **/dieukhien**.

| Nút | Thao tác |
| --- | --- |
| **Trạng thái** | Xem phiên bản, lần quét, khung giờ và tình trạng phiên GHN. |
| **Quét ngay** | Yêu cầu một lượt quét trong khung giờ khi theo dõi đang bật. |
| **Cài đặt theo dõi** | Đổi giờ, ca, chu kỳ, quét nhanh, ngưỡng báo trễ, bàn giao, theo dõi xe sắp đến, nơi nhận tin, tự lấy phiên GHN và tên người quản lý. |
| **Trợ lý AI** | Xem và bật/tắt trợ lý. Khóa dịch vụ được cài trên máy kho theo [hướng dẫn AI](HUONG-DAN-AI.md). |
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
