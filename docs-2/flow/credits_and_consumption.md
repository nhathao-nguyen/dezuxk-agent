# Chức năng: Thống kê Chi phí Credit & Hạn mức Tài khoản (Credits & Consumption)

Tài liệu này quy định đầy đủ cơ chế trích xuất mô hình và phân giải chi phí tín dụng động (Dynamic Credit Resolution) cho cả hai loại tác vụ: **Tạo Video** và **Tạo Ảnh** từ ma trận máy chủ Google Flow (`HTrJv`), kết hợp cơ chế tra cứu số dư tài khoản tổng quát (`nzlxg`).

---

> [!IMPORTANT]
> **Nguyên tắc Bất biến (Zero-Hardcoding Policy):**
> Tuyệt đối **KHÔNG GÁN CỨNG (HARDCODE)** bất kỳ con số credit, thời lượng, hay tên model nào trong mã nguồn. Toàn bộ biểu phí cho cả Video và Ảnh phải được trích xuất động tại thời điểm chạy (runtime) từ RPC `HTrJv` đối chiếu với phân hạng tài khoản (`user_tier`) lấy từ RPC `nzlxg`.

---

## 1. Phương Pháp Trích Xuất & Phân Giải Chi Phí Mô Hình Tạo Ảnh Động (`root[5]`)

Toàn bộ các mô hình tạo ảnh (Text-to-Image, Style Reference, Upsampler ảnh) được lưu trữ tại mảng **`root[5]`** của RPC `HTrJv`.

### 1.1. Cấu Trúc Payload Gốc của Model Ảnh từ Server
```json
[
  "Display_Name_String", // root[5][i][0]: Tên hiển thị động trên UI Google
  [
    [
      "ENGINE_ID",       // root[5][i][1][0][0]: Mã engine xử lý nội bộ (vd: GEM_PIX_2, NARWHAL)
      null, null, null,
      [                  // root[5][i][1][0][4]: Ma trận biểu phí theo phân hạng Tier tài khoản
        [ 1, [ [null, 0] ] ], // Tier 1 (Free)     -> 0 credit
        [ 2, [ [null, 0] ] ], // Tier 2 (Standard) -> 0 credit
        [ 3, [ [null, 0] ] ]  // Tier 3 (Pro)      -> 0 credit
      ],
      null, null, null,
      [ ... ],           // root[5][i][1][0][8]: Ma trận các chế độ phân giải hỗ trợ
      10,                // root[5][i][1][0][9]: Giới hạn hàng đợi song song
      null, null, null,
      [ [1, 2, 3, 4, 5, 2, 3] ], // root[5][i][1][0][13]: Danh sách mã enum Aspect Ratio hỗ trợ
      40,                // root[5][i][1][0][14]: Thời gian timeout (giây)
      null, null, true   // root[5][i][1][0][17]: Trạng thái kích hoạt (Active flag)
    ]
  ],
  true,                  // root[5][i][2]: Cờ mặc định (IsDefault flag)
  "backend_model_id"     // root[5][i][3]: Mã model duy nhất gửi lên API sinh ảnh
]
```

### 1.2. Thuật Toán Tính Phí Credit Động cho Ảnh
1. Lấy cấp độ `user_tier` của tài khoản người dùng từ RPC `nzlxg` (thường là `1`, `2`, hoặc `3`).
2. Với mô hình ảnh người dùng chọn, truy cập mảng `tier_matrix = model[1][0][4]`.
3. Tìm phần tử có `tier_entry[0] === user_tier`:
   * Nếu `tier_entry[1]` rỗng `[]` hoặc phần tử chi phí `cost === 0`: **0 Credits (Miễn phí)**.
   * Nếu `cost > 0`: Chi phí gốc là `cost` credits cho 1 ảnh.
4. Áp dụng hệ số nhân theo số lượng ảnh yêu cầu tạo (`count` từ 1 đến 4):
   $$\text{Total Image Credits} = \text{cost} \times \text{count}$$

---

## 2. Phương Pháp Trích Xuất & Phân Giải Chi Phí Mô Hình Tạo Video Động (`root[4]`)

Toàn bộ các mô hình tạo video (Text-to-Video, Image-to-Video, Extension, Inpainting, Upsampler video) được lưu trữ tại mảng **`root[4]`** của RPC `HTrJv`.

### 2.1. Cấu Trúc Payload Gốc của Model Video từ Server
```json
[
  "Group_Display_Name", // root[4][i][0]: Tên nhóm mô hình video (vd: Omni 1.1 Flash, Veo 3.1)
  [
    [
      "capability_task_id", // root[4][i][1][j][0]: Tác vụ & cấu hình (vd: abra_t2v_4s, veo_3_1_fast)
      null, null, null,
      [                     // root[4][i][1][j][4]: Ma trận biểu phí theo Tier
        [ 1, [ [null, 7] ] ],  // Tier 1 -> 7 credits
        [ 2, [ [null, 7] ] ],  // Tier 2 -> 7 credits
        [ 3, [ [null, 7] ] ]   // Tier 3 -> 7 credits
      ],
      null, null,
      [ ... ],              // root[4][i][1][j][7]: Cấu hình khung hình tham chiếu
      null, null, true, null,
      [ [2, 1] ],           // root[4][i][1][j][12]: Enum tỷ lệ khung hình cho phép
      null,
      120,                  // root[4][i][1][j][14]: Mã hồ sơ render / FPS
      null,
      4,                    // root[4][i][1][j][16]: Thời lượng video (giây: 4, 6, 8, 10...)
      true                  // root[4][i][1][j][17]: Khả dụng (Enabled)
    ]
  ]
]
```

### 2.2. Thuật Toán Tính Phí Credit Động cho Video
1. Xác định tác vụ video mà người dùng đang thực hiện:
   * `t2v`: Văn bản sang Video (`Text-to-Video`).
   * `i2v`: Ảnh sang Video (`Image-to-Video` đơn khung hình hoặc đầu/cuối `fl`).
   * `r2v`: Video có phong cách tham chiếu (`Reference-to-Video`).
   * `extend`: Mở rộng thêm thời lượng video đã có.
2. Lọc danh sách `variants = group[1]` theo:
   * Loại tác vụ (`task_type`).
   * Thời lượng người dùng chọn (`duration = variant[16]`, vd: 4s, 6s, 8s, 10s).
   * Độ phân giải (720p HD hoặc 360p Preview).
3. Đọc biểu phí từ `variant[4]` đối chiếu với `user_tier`:
   * Lấy giá trị `cost` từ cấu hình tương ứng.
4. Áp dụng hệ số nhân số lượng (`count` từ 1 đến 4):
   $$\text{Total Video Credits} = \text{cost}(\text{duration}, \text{task}) \times \text{count}$$

---

## 3. Bảng Tổng Hợp Chi Phí Động Phân Giải Tại Runtime (Mẫu Trích Xuất Server)

| Danh Mục | Tác Vụ / Cấu Hình Kỹ Thuật | Phân Giải Biểu Phí Động từ Server (`HTrJv`) | Quy Tắc Tính Tiền |
| :--- | :--- | :--- | :--- |
| **Ảnh (Image)** | Mọi mô hình Text-to-Image / Style Ref trong `root[5]` | `cost = 0` (Áp dụng cho mọi Tier) | **0 Credits $\times$ Count = 0 Credit (Miễn phí)** |
| **Ảnh Nâng Cấp** | Bộ Upsampler 2K / 4K trong `root[5]` | `cost = 0` (Tier 2/3) hoặc theo server | Phân giải động theo gói tài khoản |
| **Video Tiết kiệm** | Video nhanh / phác thảo 360p (`duration` 4s–10s) | `cost = 4 - 7 credits` tùy thời lượng | $\text{cost}(\text{duration}) \times \text{Count}$ |
| **Video Chuẩn** | Video HD 720p chuẩn (`duration` 4s–10s) | `cost = 7 - 15 credits` tùy thời lượng | $\text{cost}(\text{duration}) \times \text{Count}$ |
| **Video Tốc độ** | Video render nhanh (`duration` 4s–8s) | `cost = 10 - 20 credits` tùy thời lượng | $\text{cost}(\text{duration}) \times \text{Count}$ |
| **Video Điện ảnh** | Video chất lượng điện ảnh cao cấp nhất (Veo Quality) | `cost = 100 credits` (Toàn bộ thời lượng) | $100 \times \text{Count}$ credits |
| **Inpainting / Edit** | Chỉnh sửa và xóa/chèn đối tượng trên khung hình | `cost = 20 credits` (720p) / `10 credits` (360p) | 20 hoặc 10 credits trên 1 tác vụ |
| **Video Upsampler** | Nâng cấp video lên 1080p Full HD (`root[4]`) | `cost = 0 credits` cho Tier 2 & 3 | **0 Credits (Miễn phí cho gói Pro)** |
| **Video Upsampler** | Nâng cấp video lên 4K Ultra HD (`root[4]`) | `cost = 50 credits` cho Tier 3 | Khấu trừ đúng 50 credits |

---

## 4. Tra Cứu Số Dư Credit Tổng Quát của Tài Khoản (RPC `nzlxg`)

* **Endpoint:** `POST https://flow.google.com/_/AiSandboxAngularFrontend/data/batchexecute?rpcids=nzlxg`
* **Cookie cần thiết:** `OSID`, `__Secure-OSID`, `__Secure-1PSID`.
* **Cấu trúc request:**
  ```json
  [[["nzlxg", "[]", null, "generic"]]]
  ```

### Ý nghĩa từng vị trí trong mảng dữ liệu server (`result`):
```json
[1050, 1, 2, 2, null, 1050]
```
* **`result[0]` (1050):** `totalCredits` — Tổng số credit khả dụng hiện tại trong tài khoản.
* **`result[1]` (1):** `creditStatus` — Trạng thái hoạt động của ví tín dụng (`1 = Active`).
* **`result[2]` (2):** `tier` — Cấp độ tài khoản Google Labs (`1 = Free`, `2 = Standard/Pro`, `3 = Ultra`). **Dùng làm khóa tra cứu trong ma trận giá động của `HTrJv`**.
* **`result[3]` (2):** `subscriptionStatus` — Trạng thái gói thuê bao tích cực.
* **`result[5]` (1050):** `originalBalance` — Hạn mức credit ban đầu của chu kỳ hiện tại.

---

## 5. Mẫu Mã Nguồn Bóc Tách Động Cả Video & Ảnh (Go / TypeScript)

### 5.1. Triển Khai Bóc Tách Bằng Go (Backend Gateway)
```go
package catalog

import (
	"encoding/json"
	"fmt"
)

type DynamicModel struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Type        string `json:"type"` // "image" hoặc "video"
	CostCredits int    `json:"cost_credits"`
	Durations   []int  `json:"durations,omitempty"`
}

// ParseDynamicCatalog bóc tách toàn bộ model Video và Image từ RPC HTrJv
func ParseDynamicCatalog(rawHTrJv []byte, userTier int) ([]DynamicModel, error) {
	var envelope [][]interface{}
	if err := json.Unmarshal(rawHTrJv, &envelope); err != nil {
		return nil, err
	}

	innerJSON := envelope[0][2].(string)
	var rootData []interface{}
	if err := json.Unmarshal([]byte(innerJSON), &rootData); err != nil {
		return nil, err
	}

	root := rootData[0].([]interface{})
	var catalog []DynamicModel

	// 1. Bóc tách Model Tạo Ảnh từ root[5]
	if len(root) > 5 && root[5] != nil {
		imageModels := root[5].([]interface{})
		for _, item := range imageModels {
			m := item.([]interface{})
			displayName := m[0].(string)
			backendID := m[3].(string)
			
			// Tính credit động theo tier
			cost := 0
			if variants, ok := m[1].([]interface{}); ok && len(variants) > 0 {
				v := variants[0].([]interface{})
				if tiers, ok := v[4].([]interface{}); ok {
					cost = extractTierCost(tiers, userTier)
				}
			}

			catalog = append(catalog, DynamicModel{
				ID:          backendID,
				DisplayName: displayName,
				Type:        "image",
				CostCredits: cost,
			})
		}
	}

	// 2. Bóc tách Model Tạo Video từ root[4]
	if len(root) > 4 && root[4] != nil {
		videoGroups := root[4].([]interface{})
		for _, grp := range videoGroups {
			g := grp.([]interface{})
			groupName := g[0].(string)

			if variants, ok := g[1].([]interface{}); ok {
				for _, vItem := range variants {
					v := vItem.([]interface{})
					taskID := v[0].(string)
					
					var duration int
					if len(v) > 16 && v[16] != nil {
						if dFloat, ok := v[16].(float64); ok {
							duration = int(dFloat)
						}
					}

					cost := 0
					if tiers, ok := v[4].([]interface{}); ok {
						cost = extractTierCost(tiers, userTier)
					}

					catalog = append(catalog, DynamicModel{
						ID:          taskID,
						DisplayName: fmt.Sprintf("%s (%ds)", groupName, duration),
						Type:        "video",
						CostCredits: cost,
						Durations:   []int{duration},
					})
				}
			}
		}
	}

	return catalog, nil
}

func extractTierCost(tiers []interface{}, targetTier int) int {
	for _, t := range tiers {
		tArr := t.([]interface{})
		tNum := int(tArr[0].(float64))
		if tNum == targetTier {
			if costArr, ok := tArr[1].([]interface{}); ok && len(costArr) > 0 {
				c := costArr[0].([]interface{})
				if len(c) > 1 && c[1] != nil {
					return int(c[1].(float64))
				}
			}
		}
	}
	return 0
}
```

---

## 6. Sơ Đồ Quy Trình Phân Giải Chi Phí Tự Động Trước Khi Gọi API

```mermaid
flowchart TD
    A[Yêu cầu Sinh Nội dung từ Client] --> B[Tra cứu Số dư & User Tier qua RPC nzlxg]
    B --> C{Tác vụ là Video hay Ảnh?}
    C -- Ảnh (Image) --> D[Lấy Cấu hình từ root[5] của HTrJv]
    D --> E[Tính: Cost = TierCost * ImageCount]
    C -- Video --> F[Lấy Cấu hình từ root[4] của HTrJv theo Task & Duration]
    F --> G[Tính: Cost = DurationCost * VideoCount]
    E --> H{Số dư Credit >= Cost?}
    G --> H
    H -- Không đủ --> I[Trả lỗi 402: Insufficient Credits]
    H -- Đủ Credit --> J[Gửi Request lên Google Flow StreamChat]
    J --> K[Nhận Event Stream: credits_deducted]
    K --> L[Đồng bộ lại Số dư & Trả kết quả về UI Client]
```
