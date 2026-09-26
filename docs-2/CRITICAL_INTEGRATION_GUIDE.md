# Sổ Tay Kỹ Thuật Tích Hợp: Các Lưu Ý Quan Trọng, Bẫy Lỗi & Giải Pháp Xử Lý
> **Tài liệu tham chiếu chuẩn cho kỹ sư phát triển Client, SDK & AI Gateway giao tiếp với Google Gemini & Flow.**

Tài liệu này tổng hợp toàn bộ các lưu ý sống còn, các bẫy kỹ thuật ngầm (gotchas), cơ chế phòng thủ WAF của Google và thuật toán xử lý dữ liệu chuẩn xác để hệ thống của bạn vận hành ổn định 24/7 ngoài trình duyệt mà không bị khóa tài khoản hoặc lỗi ngắt kết nối.

---

> ### ⚠️ TỔNG HỢP CÁC LƯU Ý QUAN TRỌNG ĐỂ KHÔNG BỊ LỖI (QUICK CHECKLIST)
> 
> 1. **Header `X-Same-Domain: 1` và `Origin`**: Bắt buộc phải có trong mọi request để vượt qua lớp kiểm tra CORS/WAF của Google.
> 2. **Quy tắc riêng của từng RPC**: Bám sát mục *"Quy tắc Tham số Đã Xác Minh Thực Tế"* trong từng file docs. Ví dụ:
>    * Với RPC `Zzl0ze`, phải truyền đúng format `projects/<project_uuid>`, không được truyền `projects/*`.
>    * Với Gemini Chat Mới, bộ 3 context ID phải là `["", "", ""]` (mảng chuỗi rỗng) chứ không được để `null`.
>    * Với Flow, thiếu cookie `OSID` (hoặc `__Secure-OSID`) sẽ bị báo lỗi `401 Unauthorized` ngay lập tức.
> 3. **Giải mã luồng Streaming (`wrb.fr`)**: Phản hồi của Google luôn có chuỗi bảo vệ `)]}'\n` ở đầu; chỉ cần bỏ chuỗi này đi là parse JSON bình thường.
> 4. **Đóng gói hai lớp JSON**: Form field `f.req` bắt buộc phải là mảng lồng chuỗi JSON 2 tầng: `[null, JSON_STRING(inner_array)]`.
> 5. **Tải ngay file Media về máy**: URL ảnh `lh3.googleusercontent.com` và video `storage.googleapis.com` là Pre-signed URL tạm thời, sẽ hết hạn sau 12 - 24 giờ.

---

## MỤC LỤC

1. [Bảo Mật WAF & Nhận Diện Dấu Vân Tay (Browser Fingerprinting)](#1-bảo-mật-waf--nhận-diện-dấu-vân-tay-browser-fingerprinting)
2. [Vòng Đời Cookie & Cơ Chế Tự Động Xoay Vòng (Cookie Rotation)](#2-vòng-đời-cookie--cơ-chế-tự-động-xoay-vòng-cookie-rotation)
3. [Kỹ Thuật Đóng Gói Hai Lớp JSON (Double JSON Encoding)](#3-kỹ-thuật-đóng-gói-hai-lớp-json-double-json-encoding)
4. [Xử Lý Luồng Streaming & Ranh Giới Gói Tin (Chunk Buffer Boundary)](#4-xử-lý-luồng-streaming--ranh-giới-gói-tin-chunk-buffer-boundary)
5. [Quản Lý Khóa Phiên & An Toàn Tín Dụng (Credit Safety & Session Lock)](#5-quản-lý-khóa-phiên--an-toàn-tín-dụng-credit-safety--session-lock)
6. [Vòng Đời Tài Nguyên Truyền Thông & CDN Hết Hạn (Media Asset TTL)](#6-vòng-đời-tài-nguyên-truyền-thông--cdn-hết-hạn-media-asset-ttl)
7. [Bảng Tra Cứu Mã Lỗi & Cách Khắc Phục (Troubleshooting Matrix)](#7-bảng-tra-cứu-mã-lỗi--cách-khắc-phục-troubleshooting-matrix)
8. [Mẫu Triển Khai Hoàn Chỉnh (Production-Ready Boilerplate)](#8-mẫu-triển-khai-hoàn-chỉnh-production-ready-boilerplate)

---

## 1. BẢO MẬT WAF & NHẬN DIỆN DẤU VÂN TAY (BROWSER FINGERPRINTING)

Máy chủ Google áp dụng hệ thống bảo vệ đa tầng (Google Cloud Armor & BotGuard) nhằm phát hiện các yêu cầu tự động. Để không bị chặn IP hoặc chuyển hướng sang CAPTCHA, bạn bắt buộc phải tuân thủ các quy tắc sau:

### 1.1. Bộ Headers Bắt Buộc Trong Mọi Request
Thiếu bất kỳ header nào trong nhóm này sẽ dẫn đến lỗi `403 Forbidden` hoặc `400 Bad Request`:
```http
Content-Type: application/x-www-form-urlencoded;charset=UTF-8
Origin: https://gemini.google.com  (hoặc https://flow.google.com)
Referer: https://gemini.google.com/app  (hoặc https://flow.google.com/)
X-Same-Domain: 1
Sec-Fetch-Dest: empty
Sec-Fetch-Mode: cors
Sec-Fetch-Site: same-origin
```

### 1.2. Nhất Quán Dấu Vân Tay User-Agent
* **Bẫy thường gặp:** Lấy cookie từ trình duyệt Chrome thật trên Windows, nhưng khi gửi request bằng Python/Node.js lại dùng `User-Agent` mặc định của thư viện (ví dụ: `python-requests/2.31.0` hoặc `node-fetch`).
* **Hậu quả:** Hệ thống phát hiện sự không đồng nhất giữa danh tính Cookie và chữ ký Client, đánh dấu phiên là bất thường và kích hoạt khóa bảo mật.
* **Quy tắc:** Luôn sao chép chính xác `User-Agent` của trình duyệt nơi bạn trích xuất cookie:
  ```http
  User-Agent: Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36
  ```

### 1.3. Cơ Chế Cô Lập Phiên Theo Domain Trên Google Flow (Origin-Bound Cookies)
* Khác với các dịch vụ thông thường chỉ dùng Cookie trên miền dùng chung `.google.com`, Google Flow vận hành trên tên miền riêng biệt `flow.google.com`.
* **Bắt buộc:** Phải có cặp cookie được ký riêng cho host này:
  * `OSID`: Cookie định danh phiên của Flow.
  * `__Secure-OSID`: Phiên bản bảo mật cao truyền tải qua giao thức HTTPS.
* **Hậu quả nếu thiếu:** Máy chủ lập tức trả về mã lỗi `401 Unauthorized` hoặc chuyển hướng (302) về trang đăng nhập Google Accounts.

---

## 2. VÒNG ĐỜI COOKIE & CƠ CHẾ TỰ ĐỘNG XOAY VÒNG (COOKIE ROTATION)

### 2.1. Phân Loại Tuổi Thọ Của Từng Cookie

| Cookie | Domain | Tuổi thọ danh nghĩa | Tần suất thay đổi thực tế | Lưu ý kỹ thuật |
| :--- | :--- | :---: | :---: | :--- |
| `__Secure-1PSID` | `.google.com` | ~1 - 2 năm | Rất ổn định | Cookie định danh người dùng gốc. |
| `__Secure-1PSIDTS` | `.google.com` | ~1 năm | **Vài giờ đến 24 giờ** | **Rolling Timestamp Token**: Google xoay vòng liên tục để chống replay cookie cũ. |
| `OSID` | `flow.google.com` | ~1 - 2 năm | Ổn định | Bắt buộc đối với Google Flow. |
| `__Secure-OSID` | `flow.google.com` | ~1 - 2 năm | Ổn định | Bắt buộc đối với Google Flow. |
| `COMPASS` | `.google.com` | ~6 tháng | Định kỳ | Token giám sát luồng lưu lượng của Gemini. |

### 2.2. Chiến Lược Xử Lý Xoay Vòng Token Trong Code
Không được lưu chết `__Secure-1PSIDTS` trong file `.env` tĩnh. Client cần cài đặt cơ chế tự động cập nhật:
1. Khi máy chủ Google trả về Header `Set-Cookie`, kiểm tra xem có cập nhật `__Secure-1PSIDTS` hay không.
2. Nếu có, lập tức lưu đè giá trị mới vào bộ nhớ lưu trữ phiên (Cookie Jar).
3. Định kỳ (ví dụ mỗi 6 giờ), gửi một request nhẹ (như `GET /usage` hoặc RPC `nzlxg`) để kích hoạt máy chủ làm mới dấu thời gian của session.

---

## 3. KỸ THUẬT ĐÓNG GÓI HAI LỚP JSON (DOUBLE JSON ENCODING)

Cơ chế `batchexecute` và `StreamGenerate` của Google sử dụng kiến trúc đóng gói dữ liệu 2 tầng:

```
[Mảng dữ liệu nội bộ]
       │
       ▼ (BƯỚC 1: JSON.stringify lần 1)
[Chuỗi JSON đã Escape] ──> Đặt vào mảng ngoài: [null, "<CHUỖI_JSON>"]
       │
       ▼ (BƯỚC 2: JSON.stringify lần 2)
[Chuỗi Form Value của f.req]
       │
       ▼ (BƯỚC 3: URL-Encoding)
[Payload Form Body gửi qua mạng]
```

### 3.1. Mã Mẫu Đóng Gói Chuẩn (TypeScript & Python)

#### Trong TypeScript / Node.js:
```typescript
function buildFormBody(innerArray: any[], atToken: string): string {
  // Lớp 1: Chuỗi hóa mảng định vị nội bộ
  const innerJsonString = JSON.stringify(innerArray);
  
  // Lớp 2: Đặt vào mảng ngoài của Google RPC
  const outerArray = [null, innerJsonString];
  
  // Lớp 3: URLSearchParams tự động mã hóa url-encode
  const formParams = new URLSearchParams();
  formParams.append("f.req", JSON.stringify(outerArray));
  formParams.append("at", atToken);
  
  return formParams.toString();
}
```

#### Trong Python:
```python
import json
import urllib.parse

def build_form_body(inner_array: list, at_token: str) -> str:
    inner_json_string = json.dumps(inner_array, ensure_ascii=False)
    outer_array = [None, inner_json_string]
    outer_json_string = json.dumps(outer_array, ensure_ascii=False)
    
    return urllib.parse.urlencode({
        "f.req": outer_json_string,
        "at": at_token
    })
```

---

## 4. XỬ LÝ LUỒNG STREAMING & RANH GIỚI GÓI TIN (CHUNK BUFFER BOUNDARY)

Khi gửi tham số `rt=c`, Google truyền phản hồi dưới dạng dòng dữ liệu chunked.

### 4.1. Cạm Bẫy Phân Mảnh Gói Tin (Packet Fragmentation)
Một dòng JSON `[["wrb.fr", ...]]` có độ dài hàng nghìn ký tự. Trên tầng mạng TCP, dòng này thường bị chia nhỏ thành nhiều gói tin mạng. Nếu bạn parse JSON ngay khi nhận sự kiện `data`, code sẽ văng lỗi `JSONDecodeError` vì chuỗi JSON bị đứt đoạn.

### 4.2. Thuật Toán Bộ Đệm Ghép Dòng (Line-Buffering Algorithm)
Client bắt buộc phải dồn dữ liệu vào buffer và chỉ xử lý khi gặp ký tự xuống dòng `\n`:

```typescript
class GoogleStreamParser {
  private buffer: string = "";

  public feed(chunk: string, onEnvelope: (rpcId: string, payload: any) => void) {
    this.buffer += chunk;
    const lines = this.buffer.split("\n");
    
    // Giữ lại phần chưa hoàn tất (phần tử cuối) trong buffer
    this.buffer = lines.pop() || "";

    for (const rawLine of lines) {
      const line = rawLine.trim();
      // Bỏ qua dòng rỗng, dòng chứa độ dài byte, hoặc tiền tố XSSI
      if (!line || line.startsWith(")]}'") || /^\d+$/.test(line)) {
        continue;
      }

      try {
        const envelope = JSON.parse(line);
        if (Array.isArray(envelope)) {
          for (const item of envelope) {
            if (item?.[0] === "wrb.fr") {
              const rpcId = item[1];
              const innerData = JSON.parse(item[2]);
              onEnvelope(rpcId, innerData);
            }
          }
        }
      } catch (err) {
        // Bỏ qua các dòng không phải JSON định dạng chuẩn
      }
    }
  }
}
```

---

## 5. QUẢN LÝ KHÓA PHIÊN & AN TOÀN TÍN DỤNG (CREDIT SAFETY & SESSION LOCK)

### 5.1. Khóa Phiên Tránh Trừ Âm Credit Trên Google Flow (`csbIsb`)
* **Bắt buộc:** Trước khi gửi bất kỳ tác vụ tốn tín dụng nào (Veo 3.1 video, Abra image), client phải gọi RPC `csbIsb` với tham số `["projects/<PROJECT_UUID>"]`.
* **Mục đích:** Đăng ký quyền làm chủ phiên (Session Ownership). Nếu gọi song song nhiều tác vụ mà không có khóa phiên, server sẽ từ chối để tránh tranh chấp hạn mức.

### 5.2. Kiểm Tra Số Dư Khả Dụng Trước Khi Gọi Tác Vụ Lớn
* Không bao giờ gửi prompt tạo video Veo Quality (100 credits) khi chưa kiểm tra số dư.
* Luôn gọi RPC `nzlxg` trước:
  ```json
  [1050, 1, 2, 2, null, 1050]
  ```
  * `index [0]`: Tổng số credit khả dụng.
  * Nếu số dư nhỏ hơn chi phí của mô hình, hãy dừng ở phía client và thông báo cho người dùng thay vì để máy chủ Google từ chối với lỗi `INSUFFICIENT_CREDITS`.

### 5.3. Hạn Mức Tự Động Xoay Vòng 5 Giờ Trên Gemini (`/usage`)
* Gemini giới hạn tốc độ xử lý dựa trên cửa sổ xoay vòng 5 giờ (`quota5h`).
* Khi nhận mã lỗi `HTTP 429 Too Many Requests`, **tuyệt đối không retry dồn dập**. Hãy áp dụng chiến thuật **Exponential Backoff**: Chờ 2s, 4s, 8s, 16s trước khi thử lại hoặc chuyển tạm sang Model Flash nhẹ hơn.

---

## 6. VÒNG ĐỜI TÀI NGUYÊN TRUYỀN THÔNG & CDN HẾT HẠN (MEDIA ASSET TTL)

### 6.1. Thời Hạn Sống Tạm Thời (Time-To-Live)
* Tất cả URL hình ảnh (`https://lh3.googleusercontent.com/gg-bard-images/...`) và video (`https://storage.googleapis.com/flow-rendered-videos/...`) trả về từ API đều là các **liên kết tạm thời được ký điện tử (Signed URLs)**.
* **Thời gian tồn tại:** Thường chỉ hợp lệ từ **12 đến 24 giờ** (thể hiện qua marker `/ttl_1d/`). Sau thời gian này, truy cập URL sẽ trả về lỗi `HTTP 403 Access Denied`.

### 6.2. Quy Tắc Vận Hành Bắt Buộc
1. **Không lưu URL thô của Google vào cơ sở dữ liệu làm dữ liệu vĩnh viễn.**
2. Ngay khi API trả về URL thành công:
   * Client lập tức tải luồng dữ liệu nhị phân (Binary Stream) của video/ảnh về đĩa cứng nội bộ hoặc lưu trữ đám mây riêng (AWS S3, Cloudflare R2, MinIO).
   * Lưu đường dẫn lưu trữ nội bộ vào cơ sở dữ liệu.

---

## 7. BẢNG TRA CỨU MÃ LỖI & CÁCH KHẮC PHỤC (TROUBLESHOOTING MATRIX)

| Mã Lỗi / Hiện tượng | Nguyên Nhân Kỹ Thuật Gốc Rễ | Giải Pháp Khắc Phục Chuẩn Xác |
| :--- | :--- | :--- |
| **`HTTP 401 Unauthorized`** | • Thiếu cookie `OSID` / `__Secure-OSID` khi gọi Flow.<br>• Cookie `__Secure-1PSIDTS` đã hết hạn. | • Bổ sung đầy đủ 4 cookie bắt buộc của Flow.<br>• Làm mới session bằng cách đồng bộ lại cookie từ trình duyệt. |
| **`HTTP 403 Forbidden`** | • Thiếu các Header bảo vệ: `Origin`, `Referer`, `X-Same-Domain: 1`.<br>• User-Agent bị lệch so với trình duyệt lấy cookie. | • Bổ sung đủ bộ 6 Headers WAF tiêu chuẩn.<br>• Đồng bộ User-Agent chuẩn của Chrome. |
| **`HTTP 400 Bad Request`** | • Payload `f.req` chưa đóng gói đủ 2 lớp JSON.<br>• Thiếu hoặc sai lệch giá trị token `at`. | • Kiểm tra lại bước lồng JSON 2 tầng: `[null, JSON_STRING]`.<br>• Lấy lại token `at` từ `WIZ_global_data.SNlM0e`. |
| **`HTTP 429 Too Many Requests`** | Vượt quá hạn ngạch điện toán trong cửa sổ 5 giờ của Gemini. | Áp dụng Exponential Backoff (chờ lũy thừa 2s, 4s, 8s) hoặc giảm số lượng request đồng thời. |
| **`JSONDecodeError`** | Cố gắng parse JSON trực tiếp trên từng chunk streaming của TCP. | Dùng cơ chế Line Buffer: chỉ parse khi chuỗi kết thúc bằng dấu `\n` và đã gỡ tiền tố `)]}'\n`. |
| **Phản hồi trả về `[]` rỗng** | Gọi RPC `Zzl0ze` bằng wildcard `projects/*`. | Truyền đúng định dạng mã dự án cụ thể: `["projects/<PROJECT_UUID>", ...]`. |
| **Link ảnh/video không xem được** | URL Signed CDN của Google đã hết thời hạn 24 giờ. | Tải file nhị phân về lưu trữ nội bộ ngay trong lượt xử lý đầu tiên. |

---

## 8. MẪU TRIỂN KHAI HOÀN CHỈNH (PRODUCTION-READY BOILERPLATE)

Dưới đây là một mô-đun Node.js hoàn chỉnh tích hợp sẵn bộ đệm Buffer, xử lý CSRF token, đóng gói JSON 2 tầng và bắt lỗi:

```typescript
// google_rpc_client.ts
import https from "node:https";

export interface GoogleSession {
  origin: string;
  cookies: string;
  atToken: string;
  userAgent: string;
}

export class GoogleRpcClient {
  constructor(private session: GoogleSession) {}

  public async executeRpc(rpcId: string, payload: any[]): Promise<any> {
    const innerJson = JSON.stringify(payload);
    const outerPayload = JSON.stringify([[[rpcId, innerJson, null, "generic"]]]);
    
    const postData = new URLSearchParams({
      "f.req": outerPayload,
      "at": this.session.atToken
    }).toString();

    const url = new URL(
      this.session.origin.includes("flow")
        ? `/_/AiSandboxAngularFrontend/data/batchexecute?rpcids=${rpcId}&rt=c`
        : `/_/BardChatUi/data/batchexecute?rpcids=${rpcId}&rt=c`,
      this.session.origin
    );

    return new Promise((resolve, reject) => {
      const req = https.request({
        hostname: url.hostname,
        path: url.pathname + url.search,
        method: "POST",
        headers: {
          "Content-Type": "application/x-www-form-urlencoded;charset=UTF-8",
          "Origin": this.session.origin,
          "Referer": this.session.origin + "/",
          "X-Same-Domain": "1",
          "User-Agent": this.session.userAgent,
          "Cookie": this.session.cookies,
          "Content-Length": Buffer.byteLength(postData)
        }
      }, (res) => {
        if (res.statusCode !== 200) {
          return reject(new Error(`Google Server returned HTTP ${res.statusCode}`));
        }

        let buffer = "";
        res.on("data", (chunk) => {
          buffer += chunk.toString("utf8");
        });

        res.on("end", () => {
          const clean = buffer.replace(/^\)\]\}'\s*\n?/, "");
          for (const line of clean.split("\n")) {
            const trimmed = line.trim();
            if (!trimmed.startsWith("[")) continue;
            try {
              const envelope = JSON.parse(trimmed);
              if (envelope[0]?.[0] === "wrb.fr" && envelope[0]?.[1] === rpcId) {
                return resolve(JSON.parse(envelope[0][2]));
              }
            } catch {}
          }
          reject(new Error(`RPC ${rpcId} payload not found in response`));
        });
      });

      req.on("error", reject);
      req.write(postData);
      req.end();
    });
  }
}
```
