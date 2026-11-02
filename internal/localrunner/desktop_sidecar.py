"""Read-only Windows desktop sidecar for Local Agent Runner.

Reads one JSON request from stdin and writes one JSON response to stdout.
No input or window-mutating operation is implemented here.
Requires Python 3.11 and pywinauto on Windows.
"""

import base64
import json
import platform
import sys


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
        else:
            response(False, "error", "unsupported read-only operation")
    except Exception as error:
        response(False, "error", str(error))


if __name__ == "__main__":
    main()
