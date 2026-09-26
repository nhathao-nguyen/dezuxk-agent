# Gateway gọi thẳng server

Tài liệu kiến trúc tổng quát cho gateway đóng vai trò client: hệ thống của ta chủ động mở kết nối và nói chuyện với server gốc, không đi qua ứng dụng khách chính thức.

Tài liệu không gắn một protocol, một ngôn ngữ, hay một sản phẩm đích. Protocol cụ thể chỉ là cách hiện thực các hợp đồng dưới đây.

Dùng được cho dự án đã chạy một phần: mỗi chương là một lớp độc lập. Phần đã có thì đối chiếu mục “dấu hiệu đã đủ” và “nợ kỹ thuật thường gặp”. Phần chưa có thì làm đúng hợp đồng, không nhồi vào code đang chạy.

---

## 0. Mục đích và cách đọc

### Gateway này là gì

```
Caller nội bộ / hệ thống của ta
        ↓
   Facade (API ổn định của ta)
        ↓
   Session + giả lập client + codec
        ↓
   Server gốc
```

Caller không biết wire format, không biết chữ ký, không giữ session gốc. Gateway là peer của server gốc.

Đây không phải reverse proxy xen giữa app thật và server. App thật chỉ xuất hiện ở pha nghiên cứu protocol, không nằm trên đường nóng.

### Tài liệu giải quyết gì

1. Chốt ranh giới từng lớp, tránh một file vừa pack byte vừa xử lý nghiệp vụ vừa giữ token.
2. Cho nhóm đã có prototype biết lớp nào đang “chạy được” và lớp nào mới chỉ là replay.
3. Làm chuẩn đối chiếu khi server gốc đổi hành vi.

### Cách dùng khi dự án đã có một phần

Với mỗi lớp, đọc theo thứ tự:

1. Hợp đồng — lớp phải đảm bảo điều gì.
2. Mô hình khái niệm — thực thể và trạng thái.
3. Trách nhiệm bắt buộc / không được làm.
4. Dấu hiệu đã đủ — prototype có thể nhận mình xong lớp này chưa.
5. Nợ thường gặp khi làm nhỏ trước.
6. Kiểm thử tối thiểu.

Nếu prototype đang trộn nhiều lớp, đừng viết lại toàn bộ. Bọc đúng hợp đồng rồi chuyển dần. Hợp đồng ổn thì implementation có thể xấu tạm thời.

### Ba nguyên tắc xuyên suốt

- **Spec là nguồn sự thật**, packet mẫu không phải spec.
- **Tách cái bất biến và cái phải tính lại mỗi lần** (hình dạng message ≠ timestamp, nonce, sign, session secret).
- **API nội bộ không được lộ hình dạng protocol gốc.** Server đổi field thì mapper đổi, caller không đổi.

---

## 1. Bản mô tả protocol

### 1.1 Vai trò

Lớp này không chạy trong request path. Nó là mô hình đủ để máy **tạo** và **đọc** một vòng thoại hợp lệ với server gốc.

Thiếu spec, các lớp sau chỉ sao chép hành vi đã bắt được. Chạy demo được, không sống được khi hết hạn token, đổi version, hoặc lệch một field.

### 1.2 Ba tầng phải tách

Giữ tách trong tài liệu và trong code. Trộn tầng là nguyên nhân lớn nhất khiến “đổi một flag client là vỡ toàn bộ parser”.

**Tầng wire**

- Transport: tin cậy hay không, datagram hay stream, multiplex hay tuần tự.
- Đơn vị kết nối: mỗi thao tác một kết nối, hay session dài.
- Khung gói: length prefix, delimiter, fixed header, codec khung (ví dụ frame type + flags + length).
- Giới hạn kích thước, fragmentation, thứ tự byte, padding.
- Hành vi kết nối: ai mở, ai đóng, idle timeout, heartbeat thuộc transport hay thuộc ứng dụng.

**Tầng envelope**

- Vỏ chung mọi payload: loại message, version protocol, correlation id, compression, mã hóa lớp ứng dụng, flag.
- Chỗ đặt mã lỗi vận chuyển (khác mã lỗi nghiệp vụ).
- Quan hệ request/response/event: 1-1, 1-n, push từ server.

**Tầng payload**

- Ngữ nghĩa nghiệp vụ: field, kiểu, đơn vị, optional/required, default.
- Định danh thao tác: opcode, method, event, tài nguyên.
- Ràng buộc chéo giữa field.
- Side effect: đọc, ghi, vừa đọc vừa ghi.

Một file “dump hex có chú thích” hữu ích cho nghiên cứu, nhưng không thay ba tầng trên.

### 1.3 Câu hỏi spec phải trả lời hết

Đích

- Định danh máy chủ logic: host, port, path, service name, region, virtual host, topic.
- Có nhiều mặt phẳng không: auth plane, data plane, realtime plane.
- Có sticky theo session/region không.

Vòng đời thoại

- Handshake / negotiate version / capability exchange.
- Thứ tự bắt buộc trước khi gọi thao tác nghiệp vụ.
- Heartbeat: chu kỳ, ai gửi, im bao lâu thì chết.
- Kết thúc: logout tường minh, drop kết nối, hay chỉ hết hạn token.

Định danh và toàn vẹn

- Request id sinh ở đâu, ai echo lại.
- Có anti-replay không: timestamp window, nonce store.
- Integrity: checksum, HMAC, chữ ký bất đối xứng, AEAD. Ký trên canonical form nào.

Lỗi

- Lỗi nằm tầng nào.
- Tạm thời hay vĩnh viễn.
- Có kèm challenge (bước thêm) hay chỉ mã.

Giới hạn

- Rate, concurrency, kích thước, độ dài session.
- Có khác nhau theo tài khoản / loại client / vùng không.

### 1.4 Hình thức lưu spec

Spec phải máy đọc được ở mức tối thiểu của tầng payload và envelope. Văn xuôi chỉ để giải thích cái schema không nói hết.

Nên có:

- Schema hoặc mô tả struct theo từng operation.
- Bảng operation: tên nội bộ, định danh trên wire, phía gửi, side effect, idempotent hay không.
- Bảng lỗi: mã gốc, tầng, lớp lỗi nội bộ tương ứng, được retry hay không.
- Versioning: spec version gắn với tín hiệu phía server (build, banner, header, handshake field).
- Test vector: byte hoặc message đã annotate, không phải screenshot.

Quy ước đặt tên nội bộ tách khỏi tên gốc. Tên gốc đổi thì bảng map đổi, tên nội bộ giữ.

### 1.5 Packet mẫu và spec

Packet bắt được là **bằng chứng**, không phải hợp đồng.

Quy trình đúng:

1. Bắt vài vòng thoại thành công và vài vòng thất bại.
2. Tách field tĩnh / field phụ thuộc ngữ cảnh / field phải tính lại.
3. Viết quy tắc sinh field động.
4. Pack lại từ quy tắc, so với vector.
5. Chỉ khi pack/unpack qua vector mới được gọi là spec.

Nếu bước 4 chưa làm, dự án đang ở mức replay. Ghi rõ điều đó trong README nội bộ, đừng gọi là “đã reverse xong”.

### 1.6 Dấu hiệu lớp spec đã đủ

- Có người lạ đọc spec + vector, viết được pack/unpack mà không nhìn app gốc.
- Mọi operation đang dùng trong production/prototype đều có mục trong bảng operation.
- Mọi mã lỗi đã gặp đều có lớp và chính sách retry.
- Có chỗ ghi “chưa biết” — khoảng trống được đánh dấu, không bị bịa.

### 1.7 Nợ thường gặp khi đã làm một phần

- Chỉ có bộ request copy-paste chạy được hôm qua.
- Field động bị hardcode (time, device id, build).
- Không phân biệt envelope và payload.
- Chỉ mô tả đường hạnh phúc, không mô tả lỗi và timeout.
- Nhiều biến thể client (web/app/version) bị coi là một protocol.

### 1.8 Kiểm thử tối thiểu

- Pack(unpack(vector)) = vector, hoặc tương đương ngữ nghĩa nếu có field không ổn định.
- Generator sinh field động khác nhau giữa hai lần gọi.
- Parser từ chối message thiếu field bắt buộc, không im lặng gán default nguy hiểm.
- Bảng operation không có thao tác “lạ” đang được gọi trong code.

---

## 2. Lớp giả lập client

### 2.1 Vai trò

Server hiếm khi chấp nhận một peer trừu tượng. Nó chờ một **hồ sơ client**: cách bắt tay, cách đánh version, cách sắp field, cách ký, cách giữ nhịp.

Lớp này biến `(operation, params, session, profile)` thành message/kết nối hợp lệ. Ngoài lớp này, không ai được đụng wire.

### 2.2 Hồ sơ client (profile)

Profile là dữ liệu, không phải nhánh if rải trong nghiệp vụ.

Nhóm thuộc tính điển hình — không phải protocol nào cũng có hết:

- Danh tính phần mềm: tên, version, build, capability flags.
- Danh tính môi trường: nền tảng, ngôn ngữ, múi giờ, đơn vị đo.
- Danh tính thiết bị: device id, install id — nếu protocol có, phải ổn định theo session policy.
- Vận chuyển: TLS fingerprint / ALPN / HTTP phiên bản / socket option / kích thước cửa sổ. Chỉ mô hình hóa những gì đã chứng minh là server có đọc.
- Phong cách message: thứ tự field, header thừa mà server vẫn kiểm, compression.
- Đồng hồ: nguồn thời gian, độ lệch cho phép.
- Thuật toán toàn vẹn: input canonical, khóa lấy từ đâu, output gắn vào chỗ nào.

Một đích có thể có nhiều profile (nền tảng khác nhau, version cũ để tương thích). Runtime chọn profile, không nhúng profile vào mapper nghiệp vụ.

### 2.3 Cái bất biến và cái phải tính lại

**Bất biến**

- Hình dạng operation.
- Tập field bắt buộc.
- Quy tắc canonical.

**Tính lại mỗi lần hoặc mỗi session**

- Timestamp, nonce, request id.
- Chữ ký, checksum phụ thuộc payload hiện tại.
- Token ngắn, cookie ngắn.
- Key trao đổi sau handshake.
- Sequence number.

Lớp giả lập được phép đọc session secret. Không được phép tự ý đổi nghĩa nghiệp vụ. Không được phép lấy secret từ biến toàn cục “token hôm nay”.

### 2.4 Đầu ra bắt buộc

Một cổng hẹp:

```
materialize(operation, params, session, profile) → OutboundAttempt
```

`OutboundAttempt` tối thiểu gồm:

- đích đã phân giải (sau khi áp region/sticky)
- byte hoặc cấu trúc sẵn sàng ghi
- deadline đề xuất
- yêu cầu vận chuyển (kết nối mới hay tái sử dụng)
- metadata để quan sát: operation, profile id, spec version — không kèm secret

Cổng đọc vào:

```
dematerialize(raw, session, profile) → InboundMessage
```

`InboundMessage` tách envelope và payload, gắn correlation, chưa dịch sang API nội bộ.

### 2.5 Handshake và capability

Nếu protocol có vòng bắt tay, đó là operation đặc biệt thuộc lớp này hoặc lớp session, không thuộc facade.

- Kết quả handshake phải ghi vào session (khóa dẫn xuất, version chốt, feature flag).
- Handshake thất bại khác auth thất bại khác network thất bại.
- Không gọi nghiệp vụ trên session chưa `ready`, trừ operation được phép lúc `authenticating`.

### 2.6 Dấu hiệu lớp này đã đủ

- Có thể gọi một operation cốt lõi **không** phát lại nguyên packet cũ.
- Đổi profile không sửa mapper nghiệp vụ.
- Hai lần gọi liên tiếp cho hai nonce/chữ ký khác nhau, cùng ngữ nghĩa.
- Test vector được tạo từ `materialize`, không chỉ từ file bắt tay công.

### 2.7 Nợ thường gặp

- Replay nguyên request đã bắt, gọi là “client”.
- Header/fingerprint copy cứng một lần.
- Hàm ký nhận cả blob raw từ caller.
- Trộn logic “nếu response chứa captcha thì…” vào chỗ pack request.
- Một singleton client dùng chung mọi tài khoản.

### 2.8 Kiểm thử tối thiểu

- Snapshot ngữ nghĩa: cùng params + cùng clock giả + cùng nonce → wire ổn định.
- Clock/nonce thật → wire đổi đúng chỗ được đánh dấu động.
- Profile A và profile B khác nhau đúng các điểm trong bảng profile.
- `dematerialize` không panic khi field lạ; đẩy sang kênh “unmapped”.

---

## 3. Session và credential

### 3.1 Vai trò

Protocol stateless vẫn có vòng đời bí mật. Protocol stateful thì session có thể là cả kết nối.

Lớp này quản lý **ai được nói**, **đang nói bằng bí mật nào**, **có được nói tiếp không**. Không pack message. Không quyết nghĩa nghiệp vụ.

### 3.2 Hai loại bí mật

**Bí mật gốc**

- Mật khẩu, refresh token dài, client certificate, device key, enrollment secret.
- Vòng đời dài, xoay có kiểm soát, lưu vault/KMS.
- Code runtime chỉ lấy qua interface, không đọc file config thuần nếu tránh được.

**Bí mật dẫn xuất**

- Access token, cookie ngắn, session key sau handshake, ticket.
- TTL ngắn, được phép nằm store nhanh (nhớ mã hóa at-rest nếu store không tin cậy).
- Mất đi thì lấy lại từ bí mật gốc + handshake, không được bịa.

Không ghi bí mật vào log, metric label, dump lỗi, tên file artifact.

### 3.3 Định danh session

Khóa logic tối thiểu:

```
session_key = (destination_logical, account_id, device_or_client_id, profile_id)
```

Thiếu một chiều sẽ trộn ngữ cảnh: token web dùng cho profile app, hoặc region A đi kèm cookie region B.

Kèm thuộc tính:

- trạng thái
- spec/protocol version đã negotiate
- thời điểm tạo / hết hạn / lần dùng cuối
- correlation của lần auth gần nhất
- khóa độc quyền (lease) nếu server không cho hai peer một lúc

### 3.4 Máy trạng thái

Trạng thái gợi ý — đặt tên khác được, đủ nghĩa thì thôi:

| Trạng thái | Ý nghĩa | Được gọi nghiệp vụ? |
|---|---|---|
| `empty` | chưa có bí mật dẫn xuất | không |
| `authenticating` | đang handshake/login | chỉ operation auth |
| `ready` | dùng được | có |
| `refreshing` | đang xoay bí mật dẫn xuất | tùy protocol; mặc định xếp hàng |
| `invalid` | bị đá, sai secret, revoke | không; cần can thiệp |
| `quarantined` | challenge, lockout, nghi ngờ ban | không; thoát khỏi hàng nóng |

Chuyển trạng thái phải tập trung. Cấm mỗi chỗ gọi tự `if status == 401 then login()`.

### 3.5 Độc quyền và tương tranh

Câu hỏi thiết kế bắt buộc, trả lời bằng văn bản trong repo:

- Một account có hai kết nối đồng thời được không?
- Refresh song song có làm mất session không?
- Ghi có cần tuần tự theo account không?

Nếu server độc quyền:

- Mọi thao tác trên cùng `session_key` đi qua lock/lease.
- Worker giữ lease kèm heartbeat; mất worker thì lease hết, worker khác nhận.
- Không dùng connection pool “dùng chung mọi account” cho kênh có identity.

### 3.6 Phân loại thất bại liên quan session

Ánh xạ về lớp, không về mã gốc:

- `bad_credentials` — đừng retry tự động.
- `expired` — refresh hoặc login lại theo policy.
- `replay_or_clock` — chỉnh đồng hồ/nonce, không đổi mật khẩu.
- `challenge` — ra khỏi đường nóng, chờ xử lý riêng.
- `banned_or_limited` — quarantine, alert.
- `upstream_unavailable` — không đụng session.

Nhầm `expired` với `bad_credentials` là cách đốt tài khoản nhanh nhất.

### 3.7 Dấu hiệu lớp này đã đủ

- Token/cookie/key không rải trong module nghiệp vụ.
- Có state machine và bảng ánh xạ lỗi → chuyển trạng thái.
- Refresh có lock; test tranh chấp viết được.
- Có đường xoay/thu hồi bí mật gốc mà không sửa code.

### 3.8 Nợ thường gặp

- Một biến `token` global.
- Login lại trong từng hàm gọi.
- Lưu mật khẩu cạnh code.
- Không phân biệt hết hạn và sai mật khẩu.
- Session sống mãi không có TTL hay eviction.

### 3.9 Kiểm thử tối thiểu

- Hết hạn → một refresh, N request chờ, không N refresh.
- Sai bí mật gốc → `invalid`, không vòng lặp login.
- Hai worker tranh lease → chỉ một người ghi wire.
- Log sample không chứa secret (test redaction).

---

## 4. Dịch request vào / ra

### 4.1 Vai trò

Đây là sản phẩm nhìn từ bên trong tổ chức. Protocol gốc là chi tiết triển khai.

Caller nói ngôn ngữ nghiệp vụ của ta. Gateway nói ngôn ngữ server gốc. Lớp này dịch hai chiều và **ổn định mặt ta**.

### 4.2 Hai hợp đồng

**Hợp đồng inbound (ta phơi ra)**

- Đặt tên theo việc cần làm, không theo opcode/path gốc.
- Version độc lập với version protocol gốc.
- Validate trước khi đụng session: kiểu, range, enum, giới hạn kích thước.
- Idempotency key của ta, kể cả khi gốc không có.
- Không nhận raw header/wire từ caller trừ kênh debug tách biệt, có kiểm soát truy cập.

**Hợp đồng outbound đã chuẩn hóa (ta trả về)**

Ba khối:

- `data` — schema ổn định.
- `error` — lớp lỗi của ta + mã gốc tùy chọn ở `debug` (không mặc định lộ cho mọi caller).
- `meta` — correlation id, thời gian, profile, spec version, có cắt dữ liệu hay không, có unmapped hay không.

Caller production đọc `data`/`error`. `meta.debug` chỉ cho nội bộ.

### 4.3 Chiến lược map

Một operation nội bộ có thể:

- 1-1 với một call gốc
- 1-n (orchestration)
- n-1 (gộp)

Ghi rõ trong bảng operation nội bộ. Orchestration thuộc lớp này hoặc một use-case layer mỏng phía trên facade, không thuộc codec.

Quy tắc field:

- Field hiểu được → map tường minh.
- Field chưa hiểu → `unmapped` có kiểm soát hoặc drop + đếm metric. Không im lặng mất dữ liệu quan trọng.
- Đơn vị, múi giờ, enum dịch một lần, ghi trong hợp đồng inbound.
- Không đảo nghĩa để “cho tiện”: đọc không được giả thành ghi.

### 4.4 Lỗi phải thành ngôn ngữ của ta

Không trả nguyên văn thông điệp gốc cho caller nếu chưa lọc. Không coi mọi thất bại là `500`.

Lớp lỗi inbound gợi ý:

- `invalid_request` — caller sai, chưa gọi gốc.
- `unauthenticated` / `unauthorized`
- `rate_limited`
- `upstream_rejected` — gốc từ chối có chủ đích
- `upstream_unavailable` — mạng, timeout, 5xx vận chuyển
- `schema_unexpected` — gốc trả hình lạ; đây là incident phía ta
- `conflict` / `not_found` / `business` — nếu nghiệp vụ cần

`schema_unexpected` không được biến thành “thử login lại”.

### 4.5 Tương thích ngược mặt ta

Khi gốc đổi:

1. Cập nhật spec + emulator.
2. Cập nhật mapper.
3. Schema inbound chỉ đổi khi nghiệp vụ ta đổi.

Nếu phải thêm field cho caller: thêm optional, không đổi nghĩa field cũ. Version API nội bộ khi phá vỡ hợp đồng.

### 4.6 Dấu hiệu lớp này đã đủ

- Caller viết được integration mà không đọc spec gốc.
- Có bảng map operation nội bộ ↔ operation gốc.
- Có chỗ duy nhất biến mã gốc thành lớp lỗi ta.
- Thêm một field gốc không bắt buộc sửa facade public.

### 4.7 Nợ thường gặp

- Expose nguyên JSON/binary gốc.
- Caller tự truyền header “cho giống app”.
- Tên endpoint nội bộ copy path gốc.
- Lỗi gốc bubble lên nguyên văn, mỗi lần server đổi câu chữ là client nội bộ gãy.
- Không có idempotency, caller retry làm nhân đôi side effect.

### 4.8 Kiểm thử tối thiểu

- Contract test facade: request hợp lệ / thiếu field / enum lạ.
- Mapper snapshot: fixture gốc → schema ta.
- Fixture gốc thêm field lạ → không vỡ, có tín hiệu unmapped.
- Orchestration 1-n: thất bại bước giữa có hành vi xác định (abort / bù / đánh dấu partial).

---

## 5. Hạ tầng vận hành

### 5.1 Vai trò

Protocol đúng vẫn chết vì timeout chồng timeout, vì hai worker đua session, vì egress bị đối phương xếp vào lớp bot, vì retry biến đọc thành ghi hai lần.

Lớp này là kỷ luật thực thi: hàng đợi, hạn mức, kết nối, deadline, cô lập lỗi, cấu hình chạy.

### 5.2 Đơn vị cô lập

Cô lập theo đúng đơn vị identity của protocol, thường là `session_key`, không phải “một process một pool toàn cục”.

Mỗi đơn vị cần biết:

- có đang giữ kết nối dài không
- hạn mức đang dùng
- lease
- circuit state tới đích

Lỗi một account không được làm đầy hàng đợi chung tới mức account khác đói.

### 5.3 Deadline và timeout

Luôn lồng:

```
deadline caller > ngân sách gateway (map + queue) > ngân sách upstream
```

Hết deadline caller thì hủy công việc còn chờ. Không để worker tiếp tục gọi gốc rồi bỏ kết quả — trừ thao tác đã ghi nhận là fire-and-forget có audit.

Timeout phải tách:

- nối
- bắt tay
- ghi
- đọc
- idle trên session dài

Một số `timeout=30s` cho tất cả là chưa đủ.

### 5.4 Retry

Retry là chính sách theo **operation + lớp lỗi**, không phải middleware toàn cục.

Được retry mặc định: lỗi vận chuyển tạm, đọc idempotent, thao tác có idempotency key gốc hoặc của ta.

Không retry mặc định: ghi không idempotent, `bad_credentials`, `challenge`, `schema_unexpected`, `banned`.

Retry phải có jitter. Ngân sách retry nằm trong deadline caller. Ghi số lần retry vào `meta`.

### 5.5 Hạn mức

Bốn trục tối thiểu:

1. Theo credential/account
2. Theo đích (host/region)
3. Theo worker/process
4. Theo identity egress (IP/pool)

Hạn mức không chỉ “N request/phút”. Có protocol hạn theo kết nối, theo handshake, theo operation nặng.

Vượt hạn mức là lỗi `rate_limited` phía ta hoặc phía gốc — phải phân biệt nguồn để quan sát.

### 5.6 Egress và tính dính

Câu hỏi bắt buộc:

- Server có gắn session với địa chỉ ra không?
- Có cần pool IP / region không?
- IPv4/IPv6 có hành vi khác không?

Nếu có sticky: session map vào một egress identity, đổi IP phải coi như rủi ro session.

Không biến vấn đề egress thành “chỉnh profile cho đến khi qua”. Đó là hai việc khác nhau.

### 5.7 Hàng đợi và backpressure

Tách nếu side effect khác nhau:

- hàng đọc
- hàng ghi
- hàng auth/refresh (ngắn, ưu tiên, có chống bão)

Khi đầy: fail nhanh về caller, đừng ẩn trong timeout. Backpressure nhìn thấy được bằng metric độ sâu hàng và thời gian chờ.

### 5.8 Circuit breaker

Mở mạch theo đích và theo lớp lỗi. Auth fail hàng loạt không phải lý do ngắt toàn bộ đọc nếu nguyên nhân là secret hết hạn — đó là việc của session manager.

Circuit trả `upstream_unavailable` ổn định, kèm thời điểm thử lại.

### 5.9 Cấu hình chạy và kill switch

Phải tắt được theo:

- một operation nội bộ
- một profile
- một đích
- toàn bộ ghi (đọc vẫn sống, nếu nghiệp vụ cho phép)

Tắt bằng config/control plane, không bằng hotfix nhét `return`. Pin được protocol profile theo phiên bản deploy.

### 5.10 Dấu hiệu lớp này đã đủ

- Có sơ đồ deadline và bảng retry.
- Có lock/lease đúng chỗ session cần độc quyền.
- Có hạn mức theo account và theo đích.
- Có kill switch operation.
- Restart process không làm N login đồng thời (thundering herd được tính).

### 5.11 Nợ thường gặp

- Cron/loop gọi thẳng emulator, không hàng đợi.
- Retry mọi HTTP/mọi reset.
- Một connection pool cho mọi identity.
- Không có ngân sách thời gian, caller treo.
- Secret và hạn mức nằm hardcode.

### 5.12 Kiểm thử tối thiểu

- Chaos timeout: gốc chậm hơn deadline caller → hủy đúng, không leak worker.
- Retry: ghi không idempotent không bị phát hai lần khi timeout mơ hồ — có chiến lược rõ (ít nhất là từ chối đoán).
- Hạn mức: account A bão không chặn hết account B.
- Kill switch: operation tắt → facade trả lỗi xác định, không đụng gốc.

---

## 6. Quan sát để biết server đổi gì

### 6.1 Vai trò

Mô hình gọi thẳng không có app gốc chạy kèm mỗi ngày để “đối chiếu hộ”. Gateway phải tự phát hiện lệch hợp đồng.

Quan sát trả lời một câu: **bug của ta, hay phía kia đã đổi.**

### 6.2 Bốn mặt quan sát

**Mặt vận hành**

- Tỉ lệ thành công theo operation, profile, đích.
- Latency p50/p95/p99 tách queue time và upstream time.
- Độ sâu hàng, số session `ready` / `refreshing` / `quarantined`.
- Số lần refresh, số lần tranh lock.

**Mặt hợp đồng**

- Validator schema trên mọi response (và request pack, nếu làm được).
- Đếm field mới, field mất, đổi kiểu, enum lạ.
- Kích thước message lệch dải lịch sử.
- Version/banner/build gốc.

**Mặt an ninh phiên**

- Tăng đột biến `expired`, `challenge`, `banned`.
- Handshake dài bất thường, thêm vòng.
- Egress identity bị fail cao.

**Mặt nghiệp vụ**

- Cùng input lab cho output lệch ngữ nghĩa (không chỉ lệch schema).
- Partial success trong orchestration.

### 6.3 Golden traffic

Bộ thao tác cố định, chạy định kỳ trên tài khoản lab, không phải tài khoản nóng.

- Input bất biến, clock có thể giả hoặc ghi nhận.
- Diff với baseline đã review.
- Diff schema tách với diff dữ liệu (dữ liệu đổi vì thế giới đổi; schema đổi vì hợp đồng đổi).

Golden traffic là canary của protocol, không thay load test.

### 6.4 Sentinel schema

Mọi response gốc qua validator trước khi map.

- Pass → map bình thường.
- Fail → lớp lỗi `schema_unexpected`, metric, sample artifact đã che, không “cố parse cho xong” trên đường nóng nếu field mất là field bắt buộc.
- Field mới optional: cảnh báo, chưa nhất thiết chặn.

Chính sách chặn hay chỉ cảnh báo phải ghi thành văn, không để từng dev quyết.

### 6.5 Artifact

Khi lỗi lạ, giữ mẫu có hạn:

- wire đã redaction
- profile id, spec version
- correlation id
- không giữ bí mật gốc/dẫn xuất

TTL ngắn. Truy cập hạn chế. Không đẩy sample vào kênh chat công khai.

### 6.6 Alert có nghĩa

Alert theo lớp, không theo “error rate chung”:

- `schema_unexpected` tăng → trang protocol
- `bad_credentials` tăng đột biến nhiều account → secret/config
- `upstream_unavailable` một đích → mạng/đích
- `challenge` xuất hiện nơi trước không có → đổi chính sách phía gốc
- latency queue tăng, latency upstream ổn → hạ tầng ta

Mỗi alert có runbook một trang: xem metric nào, xem golden nào, quyết định freeze ghi hay chỉ pin profile.

### 6.7 Dấu hiệu lớp này đã đủ

- Có dashboard tách lớp lỗi.
- Có golden job và baseline.
- Có validator trên đường nóng hoặc shadow đủ gần nóng.
- On-call phân biệt được “họ đổi” và “ta hết hạn token” mà không đọc code.

### 6.8 Nợ thường gặp

- Chỉ log “call failed”.
- Không redaction, log thành kho secret.
- Alert error rate tổng, thức nửa đêm vì timeout mạng.
- Không có tài khoản lab, test trên tài khoản thật.
- Bắt được packet lúc prototype rồi không giữ pipeline so sánh.

### 6.9 Kiểm thử tối thiểu

- Fixture response thiếu field bắt buộc → `schema_unexpected` + metric.
- Fixture thêm field lạ → cảnh báo unmapped, facade không vỡ.
- Redaction: fixture chứa token → artifact không còn token.
- Golden job fail thì alert đúng kênh, không im.

---

## 7. Stack thực tế

### 7.1 Nguyên tắc chọn

Không chọn stack theo mốt. Chọn theo vai trò. Được phép đa ngôn ngữ nếu ranh giới lớp sạch.

Stack sai điển hình: một module vừa mở socket, vừa ký, vừa login, vừa trả JSON cho caller, vừa parse HTML lỗi.

### 7.2 Vai trò và tính chất cần có

**Spec và codec**

- Ưu tiên generate từ schema nếu protocol cho phép.
- Test vector chạy trong CI, không phụ thuộc mạng.
- Ngôn ngữ kiểu mạnh giúp, nhưng bộ test vàng quan trọng hơn.

**Giả lập client**

- Kiểm soát được transport thật sự dùng (stream, multiplex, TLS).
- Thư viện crypto già đời, ít tự viết.
- Profile là data.

**Session**

- Store có lệnh nguyên tử (lock, compare-and-set, TTL).
- Vault/KMS cho bí mật gốc.
- State machine viết tường minh, có test.

**Facade**

- Schema-first cho API nội bộ.
- Validate ở biên.
- Versioning độc lập.

**Runtime**

- Worker và hàng đợi thật, backpressure nhìn thấy được.
- Lịch deadline, không fire-and-forget mặc định.
- Pool/kết nối theo identity khi protocol đòi.

**Quan sát**

- Metric theo nhãn: operation nội bộ, lớp lỗi, đích, profile — không gắn account thô nếu không cần.
- Trace id xuyên facade → session → attempt.
- Log cấu trúc.

**Control plane**

- Flag operation, pin profile, đổi hạn mức không cần build lại nếu vận hành yêu cầu.
- Audit người bật/tắt.

### 7.3 Biên triển khai gợi ý

Ba khối đủ dùng cho hầu hết dự án nhỏ-vừa:

1. **Control + facade**: nhận caller, validate, authz nội bộ.
2. **Worker**: session, emulator, egress.
3. **Store**: session dẫn xuất, lease, hàng đợi, config.

Lab tách production: credential riêng, egress riêng, cấm golden job đụng hàng nóng.

### 7.4 Tiêu chí “đủ tốt”, không phải stack lý tưởng

Prototype được chấp nhận khi:

- ranh giới 7 lớp gọi được tên trong repo (thư mục hoặc module)
- secret không trong git
- có ít nhất một test vector pack/unpack
- có ít nhất một operation đi hết facade → gốc → schema ta
- có log correlation id

Chưa cần service mesh, chưa cần đa region, chưa cần codegen hoàn hảo.

### 7.5 Nợ stack thường gặp khi đã có một phần

- Script nghiên cứu biến thành service bằng cách gắn HTTP lên đầu file.
- Không có CI cho codec; chỉ test tay.
- Store session là file hoặc biến bộ nhớ process, restart là login bão.
- Quan sát = print.
- Không phân môi trường lab/prod.

Trả nợ theo lớp, không “đổi ngôn ngữ là xong”.

---

## 8. Ghép lớp trên đường nóng

```
Caller
  → Facade          validate, idempotency, authz nội bộ
    → Session       lấy lease, sẵn sàng hoặc refresh
      → Emulator    materialize theo spec + profile
        → Runtime   hạn mức, deadline, ghi/đọc egress
          → Server gốc
        ← Runtime
      ← Emulator    dematerialize
      ← Sentinel    schema
    ← Session       cập nhật TTL / trạng thái nếu gốc yêu cầu
  ← Facade          map data/error/meta
```

Song song, không chặn đường nóng trừ khi chính sách sentinel yêu cầu chặn:

- ghi metric/trace
- golden định kỳ
- persist artifact khi lệch hợp đồng

Quy tắc phụ thuộc:

- Facade không import codec wire.
- Emulator không biết schema API nội bộ.
- Session không biết field nghiệp vụ.
- Runtime không sửa payload.
- Quan sát đọc mọi lớp, không quyết nghĩa nghiệp vụ.

Nếu import ngược (codec biết HTTP nội bộ, facade biết cách ký), ranh giới đã vỡ — sửa hướng phụ thuộc trước khi thêm operation.

---

## 9. Mức trưởng thành cho dự án đang làm dở

Dùng bảng này để đánh giá hiện trạng, không để trang trí.

| Mức | Hiện trạng điển hình | Được phép làm | Không làm |
|---|---|---|---|
| 0 Nghiên cứu | Bắt được thoại, chưa pack lại | Ghi chú, vector thô | Gắn API public |
| 1 Replay | Gửi lại packet mẫu còn sống | Lab, tài khoản thí nghiệm | Caller thật, ghi hàng loạt |
| 2 Codec | Pack/unpack từ spec + vector | Thêm operation trên giấy | Hardcode field động mới |
| 3 Peer | `materialize` + session lab | Vài thao tác đọc | Coi như SLA |
| 4 Facade | API nội bộ ổn định | Caller nội bộ hạn chế | Expose hình gốc |
| 5 Vận hành | hạn mức, lock, retry, switch | Tăng lưu lượng có trần | Bỏ lab |
| 6 Quan sát | golden + sentinel + alert theo lớp | Cam kết duy trì | Tin “im là đúng” |

Đội ở mức 1–2 thường tưởng mình ở mức 4 vì đã có một endpoint trả dữ liệu. Đặt mức trung thực trong tài liệu dự án.

### Thứ tự trả nợ khuyến nghị

1. Viết bảng operation và đánh dấu operation nào đang replay.
2. Tách secret ra khỏi code.
3. Viết test vector cho operation đang sống.
4. Bọc facade trước khi thêm caller mới.
5. State machine session trước khi tăng concurrency.
6. Sentinel trước khi tăng lưu lượng ghi.

Không song song tất cả. Prototype nhỏ gãy vì làm hết lúc, không vì thiếu framework.

---

## 10. Việc phải trả lời bằng văn bản trong repo

Các câu sau không có đáp án phổ quát. Phải ghi cụ thể cho dự án:

1. Server có bắt buộc một peer tại một thời điểm trên một account không?
2. Field nào trên wire là bất biến, field nào tính lại?
3. Tín hiệu nào của gốc nghĩa là hết hạn, sai secret, challenge, ban?
4. Operation nào idempotent?
5. Caller được thấy mã gốc mức nào?
6. Field lạ: cảnh báo hay chặn?
7. Session có dính egress identity không?
8. Lab dùng credential nào, cấm dùng vào đâu?
9. Ai được bật lại operation đã kill switch?
10. Spec version hiện tại gắn với tín hiệu gốc nào?

Thiếu câu trả lời thì 7 lớp trên vẫn chỉ là khung.

---

## 11. Ranh giới trách nhiệm tóm tắt

| Lớp | Quyết định | Không quyết định |
|---|---|---|
| Spec | Hình dạng thoại, nghĩa field gốc | Ai được gọi, lúc nào gọi |
| Giả lập client | Cách nói như một peer hợp lệ | Nghĩa API nội bộ |
| Session | Bí mật, trạng thái, độc quyền | Pack byte, schema caller |
| Dịch vào/ra | Hợp đồng ta, map, lỗi ta | Transport, lock |
| Hạ tầng | Deadline, hạn mức, hàng, egress | Ngữ nghĩa payload |
| Quan sát | Có lệch hợp đồng / sức khỏe | Tự sửa protocol |
| Stack | Hiện thực các hợp đồng trên | Thay thế hợp đồng |

---

## 12. Định nghĩa xong

Một operation được gọi là xong khi:

- có mục spec (wire/envelope/payload) và vector
- `materialize` / `dematerialize` không replay
- đi qua session state machine
- có mặt trên facade với schema và lớp lỗi
- có chính sách retry/timeout/hạn mức
- có metric theo lớp lỗi và validator hoặc golden tương ứng

Thiếu một dòng thì đó là prototype có kiểm soát, không phải lớp đã đóng.

Tài liệu này là khung. Chi tiết protocol cụ thể nằm ở spec riêng. Khi thêm operation mới, đi hết danh sách mục 12, đừng thêm hàm gọi gốc ở một góc ngẫu nhiên.
