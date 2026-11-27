# Trợ lý AI GHN Tele

## Bật trên Telegram

Quản lý mở chat riêng với bot, dùng **/menu → AI và tra cứu → Bật trợ lý**. Có thể dùng **/ai bat** hoặc **/ai tat**. Trợ lý mặc định tắt khi nâng cấp. Lệnh báo xe và quản trị hiện tại vẫn hoạt động khi tắt trợ lý.

## Dùng trong nhóm

- **/q Xe nào sắp đến?** để hỏi số xe và chuyến gần nhất có giờ dự kiến.
- **/q Còn bao lâu chuyến E999999TEST01 tới kho?** để hỏi theo mã chuyến.
- **/soan Nhắc anh em chuẩn bị bốc hàng, giọng vui nhẹ** để nhận bản nháp.
- Có thể tag bot hoặc trả lời trực tiếp tin của bot nếu Telegram chuyển tin đó tới app. Khi Telegram bật Privacy Mode, dùng lệnh **/q@tên_bot** là cách gọi rõ nhất.

- **/giao** hiển thị tóm tắt ca từ dữ liệu tại máy kho, kể cả khi trợ lý đang tắt.
- Lệnh cũ **/hoi** và **/soantin** vẫn dùng được.

Câu hỏi về xe dùng dữ liệu lần quét mới nhất tại máy kho và hiện giờ quét. Nếu số liệu cũ hoặc thiếu, bot báo rõ. App giữ SESSION, sổ xe và dữ liệu tài xế tại máy kho. Câu trò chuyện hoặc yêu cầu soạn tin được gửi cho dịch vụ AI đã cấu hình. Chỉ nhập nội dung anh muốn gửi cho dịch vụ đó. Bot giới hạn tần suất và chỉ trả lời khi được gọi. Bản này hỗ trợ câu hỏi văn bản.

## Đổi khóa từ xa từ 2.19.2

1. Trong chat riêng của quản lý, mở **/menu → AI và tra cứu → Khóa API**. Bấm **Tải trang tạo mã** lần đầu rồi lưu tệp HTML trên điện thoại.
2. Bấm **Đổi khóa Claude** hoặc **Đổi khóa Gemini**. Sao chép mã **GHNPAIR1** hiện trong tin của bot.
3. Mở tệp tạo mã bằng trình duyệt trên điện thoại. Dán mã GHNPAIR1 và khóa API vào hai ô, rồi bấm **Tạo mã để gửi bot**.
4. Gửi lại dòng **GHNKEY1** vào đúng chat riêng. Mã ghép có hiệu lực 7 phút và dùng một lần. Sau khi bot báo đã lưu, anh có thể thử câu hỏi mới ngay.

Trang HTML tạo mã tại chỗ. Khóa lưu trên máy kho được Windows bảo vệ theo tài khoản đang chạy GHN Tele. Khóa mới ưu tiên hơn biến môi trường đã cài trước đó. Nên dùng bản trang do bot gửi hoặc bản kèm mã nguồn.

## Cấu hình trò chuyện và soạn tin trên Windows

1. Tạo API key từ Google AI Studio hoặc Claude Console nếu muốn dùng dịch vụ tương ứng.
2. Trong Windows, mở **Edit environment variables for your account** bằng tài khoản chạy máy kho. Tạo biến người dùng `GHN_GEMINI_API_KEY` hoặc `GHN_CLAUDE_API_KEY`, rồi nhập khóa trực tiếp trên Windows.
3. Đăng xuất và đăng nhập lại tài khoản Windows để tiến trình mới nhận biến, sau đó chạy lại GHN Tele. Bật trợ lý trong Telegram và thử **/soan**.

Có thể cài cả hai khóa. Câu trò chuyện và soạn tin thường ưu tiên Gemini, còn câu dài từ 320 ký tự hoặc có yêu cầu so sánh, phân tích hay lập kế hoạch ưu tiên Claude Sonnet 5. Claude dùng Haiku 4.5 cho câu thường nếu chỉ có Claude hoặc Gemini gặp lỗi tạm thời. Nếu Claude Sonnet không dùng được, bot có thể chuyển sang Gemini nếu đã cấu hình. Câu hỏi tối đa 2.400 ký tự.

Các câu hỏi về số liệu xe, giờ đến, số kiện và bàn giao ca vẫn tra tại máy kho. Mục **AI và tra cứu** trong chat riêng của quản lý hiện tên các dịch vụ có khóa đã đọc được. Khóa có mặt trên máy chưa xác nhận được quyền sử dụng hoặc hạn mức của tài khoản. Hạn mức và chi phí tùy tài khoản API.
