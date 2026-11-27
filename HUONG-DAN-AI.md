# Trợ lý AI GHN Tele

## Bật trên Telegram

Quản lý mở chat riêng với bot, dùng **/menu → AI và tra cứu → Bật trợ lý**. Có thể dùng **/ai bat** hoặc **/ai tat**. Trợ lý mặc định tắt khi nâng cấp. Lệnh báo xe và quản trị hiện tại vẫn hoạt động khi tắt trợ lý.

## Dùng trong nhóm

- **/hoi Xe nào sắp đến?** để hỏi số xe và chuyến gần nhất có giờ dự kiến.
- **/hoi Còn bao lâu chuyến E999999TEST01 tới kho?** để hỏi theo mã chuyến.
- **/soantin Nhắc anh em chuẩn bị bốc hàng, giọng vui nhẹ** để nhận bản nháp.
- Có thể tag bot hoặc trả lời trực tiếp tin của bot nếu Telegram chuyển tin đó tới app. Khi Telegram bật Privacy Mode, dùng lệnh **/hoi@tên_bot** là cách gọi rõ nhất.

Câu hỏi về xe dùng dữ liệu lần quét mới nhất tại máy kho và hiện giờ quét. Nếu số liệu cũ hoặc thiếu, bot báo rõ. App giữ SESSION, sổ xe và dữ liệu tài xế tại máy kho. Câu trò chuyện hoặc yêu cầu soạn tin được gửi cho dịch vụ AI đã cấu hình. Chỉ nhập nội dung anh muốn gửi cho dịch vụ đó. Bot giới hạn tần suất và chỉ trả lời khi được gọi. Bản này hỗ trợ câu hỏi văn bản.

## Cấu hình trò chuyện và soạn tin trên Windows

1. Tạo API key từ Google AI Studio hoặc Claude Console nếu muốn dùng dịch vụ tương ứng.
2. Trong Windows, mở **Edit environment variables for your account** bằng tài khoản chạy máy kho. Tạo biến người dùng `GHN_GEMINI_API_KEY` hoặc `GHN_CLAUDE_API_KEY`, rồi nhập khóa trực tiếp trên Windows.
3. Đăng xuất và đăng nhập lại tài khoản Windows để tiến trình mới nhận biến, sau đó chạy lại GHN Tele. Bật trợ lý trong Telegram và thử **/soantin**.

Có thể cài cả hai khóa. Câu trò chuyện và soạn tin thường ưu tiên Gemini, còn câu dài từ 320 ký tự hoặc có yêu cầu so sánh, phân tích hay lập kế hoạch ưu tiên Claude Sonnet 5. Claude dùng Haiku 4.5 cho câu thường nếu chỉ có Claude hoặc Gemini gặp lỗi tạm thời. Nếu Claude Sonnet không dùng được, bot có thể chuyển sang Gemini nếu đã cấu hình. Câu hỏi tối đa 2.400 ký tự.

Các câu hỏi về số liệu xe, giờ đến, số kiện và bàn giao ca vẫn tra tại máy kho. Mục **AI và tra cứu** trong chat riêng của quản lý hiện tên các dịch vụ có khóa đã đọc được. Khóa có mặt trên máy chưa xác nhận được quyền sử dụng hoặc hạn mức của tài khoản. Hạn mức và chi phí tùy tài khoản API.
