"""Windows desktop sidecar for Local Agent Runner.

Reads one JSON request from stdin and writes one JSON response to stdout.
Read operations (status, windows, screenshot, inspect) are always available.
The "action" operation is only reached through the runner after a user has
approved that exact action; it validates every argument before it touches
the desktop. Requires Python 3.11 and pywinauto on Windows.
"""

import base64
import json
import platform
import sys
import re


ALLOWED_HOTKEYS = {
    "CTRL+A": "{CTRL}a",
    "CTRL+C": "{CTRL}c",
    "CTRL+V": "{CTRL}v",
    "CTRL+S": "{CTRL}s",
    "ALT+TAB": "%{TAB}",
    "ESC": "{ESC}",
}
SCROLL_DIRECTIONS = {"up", "down", "left", "right"}
CLICK_ACTIONS = {"desktop.click", "desktop.double_click"}
MAX_TYPED_TEXT = 4000
MAX_SCREENSHOT_AGE_MS = 2000


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
            target = resolve_window(desktop, request.get("window"))
            elements = []
            for control in target.descendants():
                name = control.window_text().strip()
                if name:
                    elements.append({"name": name, "control_type": control.element_info.control_type, "automation_id": control.element_info.automation_id})
            response(True, "desktop_ready", data={"elements": elements[:500]})
        elif operation == "action":
            execute_action(desktop, request.get("options") or {})
        else:
            response(False, "error", "unsupported operation")
    except Exception as error:
        response(False, "error", str(error))


def normalize_hotkey(value):
    """Return the canonical allowlisted form ("CTRL+A") for a hotkey.

    Accepts either a "ctrl+a" string or a list of key names such as
    ["ctrl", "a"], which is what the frontend sends.
    """
    if isinstance(value, str):
        parts = value.split("+")
    elif isinstance(value, (list, tuple)) and value:
        parts = list(value)
    else:
        raise ValueError("hotkey is not on the allowlist")
    if not all(isinstance(part, str) and part.strip() for part in parts):
        raise ValueError("hotkey is not on the allowlist")
    combo = "+".join(part.strip().upper() for part in parts)
    if combo not in ALLOWED_HOTKEYS:
        raise ValueError("hotkey is not on the allowlist")
    return combo


def validate_action_arguments(action, arguments):
    """Validate action arguments without touching the desktop.

    Runs before the window is resolved so that a bad request fails fast and
    with a precise message. Returns the normalized arguments.
    """
    if not isinstance(arguments, dict):
        raise ValueError("arguments must be an object")
    if action == "desktop.type":
        text = arguments.get("text")
        if not isinstance(text, str) or not text or len(text) > MAX_TYPED_TEXT:
            raise ValueError("text is required and must be at most 4000 characters")
        return {"text": text}
    if action == "desktop.hotkey":
        return {"keys": normalize_hotkey(arguments.get("keys"))}
    if action == "desktop.scroll":
        try:
            amount = int(arguments.get("amount", 1))
        except (TypeError, ValueError):
            raise ValueError("scroll amount must be between 1 and 20")
        if amount < 1 or amount > 20:
            raise ValueError("scroll amount must be between 1 and 20")
        direction = arguments.get("direction", "down")
        if direction not in SCROLL_DIRECTIONS:
            raise ValueError("invalid scroll direction")
        return {"amount": amount, "direction": direction}
    return {}


def point_in_rect(x, y, rect):
    return rect.left <= x < rect.right and rect.top <= y < rect.bottom


def root_window_at_point(x, y):
    """Return the handle of the top-level window that is visible at a screen point."""
    import ctypes
    from ctypes import wintypes

    user32 = ctypes.windll.user32
    user32.WindowFromPoint.argtypes = [wintypes.POINT]
    user32.WindowFromPoint.restype = wintypes.HWND
    user32.GetAncestor.argtypes = [wintypes.HWND, wintypes.UINT]
    user32.GetAncestor.restype = wintypes.HWND
    hwnd = user32.WindowFromPoint(wintypes.POINT(int(x), int(y)))
    if not hwnd:
        return None
    return user32.GetAncestor(hwnd, 2)  # GA_ROOT


def execute_action(desktop, options):
    action = options.get("action")
    if action not in {"desktop.focus_window", "desktop.scroll", "desktop.type", "desktop.hotkey"} | CLICK_ACTIONS:
        raise ValueError("unsupported interaction action")
    arguments = validate_action_arguments(action, options.get("arguments") or {})
    window = resolve_window(desktop, options.get("window"))
    target = options.get("target") or {}
    if (action in ({"desktop.type", "desktop.hotkey"} | CLICK_ACTIONS)) and is_sensitive_window(window):
        raise ValueError("interaction with password or credential windows is blocked")

    if action == "desktop.focus_window":
        window.set_focus()
    elif action in CLICK_ACTIONS:
        double = action == "desktop.double_click"
        kind, value = resolve_click_target(window, target)
        if kind == "point":
            from pywinauto import mouse
            click = mouse.double_click if double else mouse.click
            click(coords=value)
        else:
            (value.double_click_input if double else value.click_input)()
    elif action == "desktop.scroll":
        # pywinauto's "amount" is the unit ("line" or "page"); "count" is how many.
        window.scroll(direction=arguments["direction"], amount="line", count=arguments["amount"])
    elif action == "desktop.type":
        control = resolve_control(window, target) if target else window
        control.type_keys(arguments["text"], with_spaces=True, set_foreground=True)
    elif action == "desktop.hotkey":
        from pywinauto.keyboard import send_keys
        send_keys(ALLOWED_HOTKEYS[arguments["keys"]])
    response(True, "completed", data={"action": action, "verified": False})


def resolve_window(desktop, value):
    if not value:
        raise ValueError("window is required")
    text = str(value)
    if text.isascii() and text.isdigit():
        try:
            window = desktop.window(handle=int(text))
            exists = window.exists(timeout=0.5)
        except Exception:
            raise ValueError("window handle is invalid")
        if not exists:
            raise ValueError("window handle is invalid")
        return window
    matches = [item for item in desktop.windows() if item.window_text().strip() == text]
    if len(matches) != 1:
        raise ValueError("window title must match exactly one open window")
    return matches[0]


def resolve_control(window, target):
    """Find exactly one UI element by automation_id, name or control_type."""
    target_type = target.get("type")
    value = target.get("value")
    if target_type == "automation_id":
        controls = window.descendants(automation_id=str(value))
    elif target_type == "name":
        controls = window.descendants(title=str(value))
    elif target_type == "control_type":
        controls = window.descendants(control_type=str(value))
    else:
        raise ValueError("target type must be automation_id, name, or control_type")
    if len(controls) != 1:
        raise ValueError("target must resolve to exactly one UI element")
    return controls[0]


def resolve_click_target(window, target):
    """Return ("point", (x, y)) or ("control", element) for a click.

    Nothing is clicked here. A coordinate target must lie inside the approved
    window and that window must be the one actually visible at the point, so a
    click can never land on another application.
    """
    if target.get("type") != "coordinate":
        return "control", resolve_control(window, target)
    try:
        x, y = int(target["x"]), int(target["y"])
        age = int(target.get("screenshot_age_ms", MAX_SCREENSHOT_AGE_MS + 1))
    except (KeyError, TypeError, ValueError):
        raise ValueError("coordinate target requires integer x and y")
    from PIL import ImageGrab
    image = ImageGrab.grab(all_screens=True)
    expected = target.get("screen_size") or []
    if age > MAX_SCREENSHOT_AGE_MS or expected != [image.width, image.height] or not (0 <= x < image.width and 0 <= y < image.height):
        raise ValueError("coordinate target is stale, mismatched, or outside the screen")
    if not point_in_rect(x, y, window.rectangle()):
        raise ValueError("coordinate target is outside the approved window")
    if root_window_at_point(x, y) != window.handle:
        raise ValueError("coordinate target is covered by another window")
    return "point", (x, y)


def is_sensitive_window(window):
    text = window.window_text().lower()
    return bool(re.search(r"password|credential|otp|one[- ]time|authenticator|password manager", text))


if __name__ == "__main__":
    main()
