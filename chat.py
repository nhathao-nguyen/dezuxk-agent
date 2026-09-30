"""
Interactive & Fast Chat CLI for Dezuxk AI Gateway
Usage:
    python chat.py
    python chat.py "So sánh iPhone 16 Pro và Galaxy S25 Ultra"
"""

import sys
import json
import urllib.request

# Ensure UTF-8 output on Windows terminal
sys.stdout.reconfigure(encoding='utf-8')

GATEWAY_URL = "http://127.0.0.1:8080/v1/chat/completions"
API_KEY = "sk-dez-12f564ddef831e78546a198cd56f4deb"
DEFAULT_MODEL = "gemini-3.8-flash"

def ask_gemini(prompt: str, model: str = DEFAULT_MODEL, stream: bool = True):
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

    req = urllib.request.Request(
        GATEWAY_URL,
        data=json.dumps(payload, ensure_ascii=False).encode("utf-8"),
        headers=headers,
        method="POST"
    )

    if stream:
        print(f"\n[Gemini ({model}) Đang trả lời...]:\n")
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
                            content = delta.get("content", "")
                            if content:
                                sys.stdout.write(content)
                                sys.stdout.flush()
                    except json.JSONDecodeError:
                        continue
            print("\n")
        except Exception as e:
            print(f"\n[Lỗi kết nối]: {e}")
    else:
        try:
            with urllib.request.urlopen(req, timeout=120) as resp:
                data = json.loads(resp.read().decode("utf-8"))
                content = data["choices"][0]["message"]["content"]
                print(f"\n[Gemini ({model})]:\n{content}\n")
        except Exception as e:
            print(f"\n[Lỗi kết nối]: {e}")

def main():
    if len(sys.argv) > 1:
        prompt = " ".join(sys.argv[1:])
        ask_gemini(prompt)
        return

    print("=======================================================")
    print("      DEZUXK AI GATEWAY - CHAT CLI TEST TOOL           ")
    print("=======================================================")
    print(f"Gateway URL: {GATEWAY_URL}")
    print(f"Model mặc định: {DEFAULT_MODEL}")
    print("Gõ 'exit' hoặc 'quit' để thoát.\n")

    while True:
        try:
            user_input = input("Bạn: ").strip()
            if not user_input:
                continue
            if user_input.lower() in ("exit", "quit", "q"):
                print("Tạm biệt!")
                break
            ask_gemini(user_input)
        except (KeyboardInterrupt, EOFError):
            print("\nĐã thoát.")
            break

if __name__ == "__main__":
    main()
