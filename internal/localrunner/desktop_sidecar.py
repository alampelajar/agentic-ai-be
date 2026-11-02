"""Read-only Windows desktop sidecar for Local Agent Runner.

Reads one JSON request from stdin and writes one JSON response to stdout.
No input or window-mutating operation is implemented here.
Requires Python 3.11 and pywinauto on Windows.
"""

import base64
import json
import platform
import sys
import re


def response(ok, status=None, message=None, data=None):
    result = {"ok": ok}
    if status:
        result["status"] = status
    if message:
        result["message"] = message
    if data is not None:
        result["data"] = data
    print(json.dumps(result), flush=True)


def main():
    try:
        request = json.loads(sys.stdin.readline())
        operation = request.get("operation")
        if platform.system() != "Windows":
            response(False, "disconnected", "Windows desktop is unavailable")
            return

        from pywinauto import Desktop
        desktop = Desktop(backend="uia")

        if operation == "status":
            response(True, "desktop_ready", data={"device_name": platform.node(), "windows_version": platform.version(), "sidecar": "ready"})
        elif operation == "windows":
            windows = []
            for window in desktop.windows():
                title = window.window_text().strip()
                if title:
                    windows.append({"handle": window.handle, "title": title, "class_name": window.class_name()})
            response(True, "desktop_ready", data={"windows": windows[:200]})
        elif operation == "screenshot":
            from io import BytesIO
            from PIL import ImageGrab
            image = ImageGrab.grab(all_screens=True)
            buffer = BytesIO()
            image.save(buffer, format="PNG")
            encoded = base64.b64encode(buffer.getvalue()).decode("ascii")
            response(True, "desktop_ready", data={"mime_type": "image/png", "base64": encoded})
        elif operation == "inspect":
            target = desktop.window(handle=int(request.get("window")))
            elements = []
            for control in target.descendants():
                name = control.window_text().strip()
                if name:
                    elements.append({"name": name, "control_type": control.element_info.control_type, "automation_id": control.element_info.automation_id})
            response(True, "desktop_ready", data={"elements": elements[:500]})
        elif operation == "action":
            execute_action(desktop, request.get("options") or {})
        else:
            response(False, "error", "unsupported read-only operation")
    except Exception as error:
        response(False, "error", str(error))


def execute_action(desktop, options):
    action = options.get("action")
    window = resolve_window(desktop, options.get("window"))
    target = options.get("target") or {}
    arguments = options.get("arguments") or {}
    if action in {"desktop.type", "desktop.hotkey"} and is_sensitive_window(window):
        raise ValueError("interaction with password or credential windows is blocked")

    if action == "desktop.focus_window":
        window.set_focus()
    elif action in {"desktop.click", "desktop.double_click"}:
        control = resolve_target(window, target)
        method = control.double_click_input if action == "desktop.double_click" else control.click_input
        method()
    elif action == "desktop.scroll":
        amount = int(arguments.get("amount", 1))
        if amount < 1 or amount > 20:
            raise ValueError("scroll amount must be between 1 and 20")
        direction = arguments.get("direction", "down")
        if direction not in {"up", "down", "left", "right"}:
            raise ValueError("invalid scroll direction")
        window.scroll(direction=direction, amount=amount)
    elif action == "desktop.type":
        text = arguments.get("text")
        if not isinstance(text, str) or not text or len(text) > 4000:
            raise ValueError("text is required and must be at most 4000 characters")
        control = resolve_target(window, target) if target else window
        control.type_keys(text, with_spaces=True, set_foreground=True)
    elif action == "desktop.hotkey":
        value = str(arguments.get("keys", "")).upper()
        if value not in {"CTRL+A", "CTRL+C", "CTRL+V", "CTRL+S", "ALT+TAB", "ESC"}:
            raise ValueError("hotkey is not on the allowlist")
        from pywinauto.keyboard import send_keys
        hotkeys = {"CTRL+A": "{CTRL}a", "CTRL+C": "{CTRL}c", "CTRL+V": "{CTRL}v", "CTRL+S": "{CTRL}s", "ALT+TAB": "%{TAB}", "ESC": "{ESC}"}
        send_keys(hotkeys[value])
    else:
        raise ValueError("unsupported interaction action")
    response(True, "completed", data={"action": action, "verified": False})


def resolve_window(desktop, value):
    if not value:
        raise ValueError("window is required")
    if str(value).isdigit():
        handle = int(value)
        window = desktop.window(handle=handle)
        if not window.exists(timeout=0.5):
            raise ValueError("window handle is invalid")
        return window
    matches = [item for item in desktop.windows() if item.window_text().strip() == str(value)]
    if len(matches) != 1:
        raise ValueError("window title must match exactly one open window")
    return matches[0]


def resolve_target(window, target):
    target_type = target.get("type")
    value = target.get("value")
    if target_type == "automation_id":
        controls = window.descendants(automation_id=str(value))
    elif target_type == "name":
        controls = window.descendants(title=str(value))
    elif target_type == "control_type":
        controls = window.descendants(control_type=str(value))
    elif target_type == "coordinate":
        from PIL import ImageGrab
        x, y = int(target.get("x", -1)), int(target.get("y", -1))
        age = int(target.get("screenshot_age_ms", 999999))
        expected = target.get("screen_size") or []
        image = ImageGrab.grab(all_screens=True)
        if age > 2000 or expected != [image.width, image.height] or not (0 <= x < image.width and 0 <= y < image.height):
            raise ValueError("coordinate target is stale, mismatched, or outside the screen")
        from pywinauto import mouse
        mouse.click(coords=(x, y))
        return window
    else:
        raise ValueError("target type must be automation_id, name, control_type, or coordinate")
    if len(controls) != 1:
        raise ValueError("target must resolve to exactly one UI element")
    return controls[0]


def is_sensitive_window(window):
    text = window.window_text().lower()
    return bool(re.search(r"password|credential|otp|one[- ]time|authenticator|password manager", text))


if __name__ == "__main__":
    main()
