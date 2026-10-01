"""
Interactive & Fast Chat / Agent CLI for Dezuxk AI Gateway
Usage:
    python chat.py
    python chat.py "So sánh iPhone 16 Pro và Galaxy S25 Ultra"
    python chat.py --test-tool "Thời tiết tại Hà Nội hôm nay thế nào?"
"""

import sys
import json
import urllib.request

# Ensure UTF-8 output on Windows terminal
sys.stdout.reconfigure(encoding='utf-8')

GATEWAY_URL = "http://127.0.0.1:8080/v1/chat/completions"
API_KEY = "sk-dez-12f564ddef831e78546a198cd56f4deb"
DEFAULT_MODEL = "gemini-3.8-flash"

SAMPLE_TOOLS = [
    {
        "type": "function",
        "function": {
            "name": "get_current_weather",
            "description": "Lấy thông tin thời tiết hiện tại cho một địa điểm",
            "parameters": {
                "type": "object",
                "properties": {
                    "location": {
                        "type": "string",
                        "description": "Tên thành phố (ví dụ: Hanoi, Ho Chi Minh, Tokyo)"
                    },
                    "unit": {
                        "type": "string",
                        "enum": ["celsius", "fahrenheit"],
                        "description": "Đơn vị nhiệt độ"
                    }
                },
                "required": ["location"]
            }
        }
    },
    {
        "type": "function",
        "function": {
            "name": "read_workspace_file",
            "description": "Đọc nội dung một tập tin mã nguồn trong thư mục làm việc",
            "parameters": {
                "type": "object",
                "properties": {
                    "path": {
                        "type": "string",
                        "description": "Đường dẫn tương đối tới tệp tin"
                    }
                },
                "required": ["path"]
            }
        }
    }
]

def ask_gemini(prompt: str, model: str = DEFAULT_MODEL, stream: bool = True, tools: list = None):
    headers = {
        "Authorization": f"Bearer {API_KEY}",
        "Content-Type": "application/json; charset=utf-8"
    }
    payload = {
        "model": model,
        "stream": stream,
        "messages": [
            {"role": "user", "content": prompt}
        ]
    }
    if tools:
        payload["tools"] = tools

    req = urllib.request.Request(
        GATEWAY_URL,
        data=json.dumps(payload, ensure_ascii=False).encode("utf-8"),
        headers=headers,
        method="POST"
    )

    if stream:
        tool_label = " [Kèm Agent Tools]" if tools else ""
        print(f"\n[Gemini ({model}){tool_label} Đang phản hồi...]:\n")
        try:
            with urllib.request.urlopen(req, timeout=120) as resp:
                for line in resp:
                    line_str = line.decode("utf-8").strip()
                    if not line_str.startswith("data: "):
                        continue
                    data_part = line_str[6:]
                    if data_part == "[DONE]":
                        break
                    try:
                        chunk = json.loads(data_part)
                        choices = chunk.get("choices", [])
                        if choices:
                            delta = choices[0].get("delta", {})
                            # 1. Hiển thị Thinking / Reasoning Content (màu xám)
                            reasoning = delta.get("reasoning_content", "")
                            if reasoning:
                                sys.stdout.write(f"\033[90m{reasoning}\033[0m")
                                sys.stdout.flush()

                            # 2. Hiển thị Content trả lời thông thường
                            content = delta.get("content", "")
                            if content:
                                sys.stdout.write(content)
                                sys.stdout.flush()

                            # 3. Hiển thị Tool Calls (màu vàng)
                            tool_calls = delta.get("tool_calls", [])
                            if tool_calls:
                                for tc in tool_calls:
                                    func = tc.get("function", {})
                                    fname = func.get("name", "")
                                    fargs = func.get("arguments", "")
                                    print(f"\n\033[93m[🛠️ Agent Tool Call]: {fname}({fargs})\033[0m")
                    except json.JSONDecodeError:
                        continue
            print("\n")
        except Exception as e:
            print(f"\n[Lỗi kết nối]: {e}")
    else:
        try:
            with urllib.request.urlopen(req, timeout=120) as resp:
                data = json.loads(resp.read().decode("utf-8"))
                choice = data["choices"][0]
                message = choice.get("message", {})
                content = message.get("content", "")
                tool_calls = message.get("tool_calls", [])
                
                if content:
                    print(f"\n[Gemini ({model})]:\n{content}\n")
                if tool_calls:
                    for tc in tool_calls:
                        func = tc.get("function", {})
                        print(f"\033[93m[🛠️ Agent Tool Call]: {func.get('name')}({func.get('arguments')})\033[0m")
        except Exception as e:
            print(f"\n[Lỗi kết nối]: {e}")

def main():
    if len(sys.argv) > 1:
        if sys.argv[1] == "--test-tool":
            prompt = " ".join(sys.argv[2:]) if len(sys.argv) > 2 else "Thời tiết tại Hà Nội hôm nay thế nào?"
            print(f"[*] Đang kiểm thử Tool Calling với công cụ mẫu...")
            ask_gemini(prompt, tools=SAMPLE_TOOLS)
            return
        prompt = " ".join(sys.argv[1:])
        ask_gemini(prompt)
        return

    print("=======================================================")
    print("      DEZUXK AI GATEWAY - CHAT & AGENT CLI TOOL        ")
    print("=======================================================")
    print(f"Gateway URL: {GATEWAY_URL}")
    print(f"Model mặc định: {DEFAULT_MODEL}")
    print("Các lệnh đặc biệt:")
    print("  /tool <câu hỏi> : Thử nghiệm gọi lệnh kèm Agent Tools mẫu")
    print("  'exit' hoặc 'quit' để thoát.\n")

    while True:
        try:
            user_input = input("Bạn: ").strip()
            if not user_input:
                continue
            if user_input.lower() in ("exit", "quit", "q"):
                print("Tạm biệt!")
                break
            if user_input.startswith("/tool"):
                sub_prompt = user_input[5:].strip()
                if not sub_prompt:
                    sub_prompt = "Thời tiết tại Tokyo hiện tại ra sao?"
                ask_gemini(sub_prompt, tools=SAMPLE_TOOLS)
                continue

            ask_gemini(user_input)
        except (KeyboardInterrupt, EOFError):
            print("\nĐã thoát.")
            break

if __name__ == "__main__":
    main()
