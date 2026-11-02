"""Unit tests for desktop_sidecar.py.

Run from this directory with the sidecar's Python environment:
    python -m unittest desktop_sidecar_test -v

Every desktop-touching call (mouse, screen capture, window hit-testing) is
patched, so these tests never click, type or scroll anything.
"""

import unittest
from types import SimpleNamespace
from unittest import mock

import desktop_sidecar as sidecar


class FakeWindow:
    def __init__(self, handle=100, title="Notes", rect=(0, 0, 800, 600)):
        self.handle = handle
        self._title = title
        self._rect = SimpleNamespace(left=rect[0], top=rect[1], right=rect[2], bottom=rect[3])
        self.calls = []

    def window_text(self):
        return self._title

    def rectangle(self):
        return self._rect

    def exists(self, timeout=0):
        return True

    def set_focus(self):
        self.calls.append(("set_focus",))

    def scroll(self, **kwargs):
        self.calls.append(("scroll", kwargs))

    def click_input(self):
        self.calls.append(("click_input",))

    def double_click_input(self):
        self.calls.append(("double_click_input",))

    def type_keys(self, *args, **kwargs):
        self.calls.append(("type_keys", args, kwargs))


class FakeDesktop:
    def __init__(self, window=None, explode=None):
        self._window = window
        self._explode = explode
        self.window_calls = 0

    def window(self, handle=None):
        self.window_calls += 1
        if self._explode:
            raise self._explode
        return self._window

    def windows(self):
        return [self._window] if self._window else []


class HotkeyTests(unittest.TestCase):
    def test_accepts_strings_and_key_lists(self):
        for value in ("ctrl+a", "CTRL+A", " ctrl + a ", ["ctrl", "a"], ["Ctrl", " A "], ("ctrl", "c"), "esc", ["esc"], ["alt", "tab"], ["ctrl+s"]):
            with self.subTest(value=value):
                self.assertIn(sidecar.normalize_hotkey(value), sidecar.ALLOWED_HOTKEYS)

    def test_rejects_everything_else(self):
        for value in (["alt", "f4"], "alt+f4", [], "", None, 5, ["ctrl", 1], ["ctrl", ""], ["ctrl", "shift", "a"], {"keys": "ctrl+a"}, ["win", "r"]):
            with self.subTest(value=value):
                with self.assertRaisesRegex(ValueError, "allowlist"):
                    sidecar.normalize_hotkey(value)


class ArgumentValidationTests(unittest.TestCase):
    def test_type_requires_bounded_text(self):
        self.assertEqual(sidecar.validate_action_arguments("desktop.type", {"text": "hi"}), {"text": "hi"})
        for arguments in ({}, {"text": ""}, {"text": 5}, {"text": "x" * 4001}):
            with self.subTest(arguments=arguments):
                with self.assertRaises(ValueError):
                    sidecar.validate_action_arguments("desktop.type", arguments)

    def test_scroll_bounds_and_direction(self):
        self.assertEqual(sidecar.validate_action_arguments("desktop.scroll", {}), {"amount": 1, "direction": "down"})
        self.assertEqual(sidecar.validate_action_arguments("desktop.scroll", {"amount": "3", "direction": "up"}), {"amount": 3, "direction": "up"})
        for arguments in ({"amount": 0}, {"amount": 21}, {"amount": "many"}, {"amount": None}, {"direction": "diagonal"}):
            with self.subTest(arguments=arguments):
                with self.assertRaises(ValueError):
                    sidecar.validate_action_arguments("desktop.scroll", arguments)

    def test_arguments_must_be_an_object(self):
        with self.assertRaises(ValueError):
            sidecar.validate_action_arguments("desktop.type", ["text"])


class PointInRectTests(unittest.TestCase):
    def test_edges(self):
        rect = SimpleNamespace(left=10, top=20, right=110, bottom=220)
        self.assertTrue(sidecar.point_in_rect(10, 20, rect))
        self.assertTrue(sidecar.point_in_rect(109, 219, rect))
        self.assertFalse(sidecar.point_in_rect(110, 100, rect))
        self.assertFalse(sidecar.point_in_rect(50, 220, rect))
        self.assertFalse(sidecar.point_in_rect(9, 100, rect))


class ResolveWindowTests(unittest.TestCase):
    def test_invalid_handle_has_a_clear_message(self):
        com_error = Exception("(-2147220991, 'An event was unable to invoke any of the subscribers')")
        with self.assertRaisesRegex(ValueError, "^window handle is invalid$"):
            sidecar.resolve_window(FakeDesktop(explode=com_error), "999999999")

        missing = FakeWindow()
        missing.exists = lambda timeout=0: False
        with self.assertRaisesRegex(ValueError, "^window handle is invalid$"):
            sidecar.resolve_window(FakeDesktop(missing), "100")

    def test_title_must_be_unique_and_window_required(self):
        window = FakeWindow(title="Notes")
        self.assertIs(sidecar.resolve_window(FakeDesktop(window), "Notes"), window)
        with self.assertRaisesRegex(ValueError, "exactly one"):
            sidecar.resolve_window(FakeDesktop(window), "Other")
        with self.assertRaisesRegex(ValueError, "required"):
            sidecar.resolve_window(FakeDesktop(window), "")


class ExecuteActionTests(unittest.TestCase):
    def setUp(self):
        self.responses = []
        patches = [
            mock.patch.object(sidecar, "response", lambda *a, **k: self.responses.append((a, k))),
            mock.patch("pywinauto.mouse.click"),
            mock.patch("pywinauto.mouse.double_click"),
            mock.patch("pywinauto.keyboard.send_keys"),
            mock.patch("PIL.ImageGrab.grab", return_value=SimpleNamespace(width=1920, height=1080)),
        ]
        self.click, self.double_click, self.send_keys = None, None, None
        started = [p.start() for p in patches]
        self.click, self.double_click, self.send_keys = started[1], started[2], started[3]
        for p in patches:
            self.addCleanup(p.stop)

    def coordinate(self, **overrides):
        target = {"type": "coordinate", "x": 100, "y": 100, "screenshot_age_ms": 500, "screen_size": [1920, 1080]}
        target.update(overrides)
        return target

    def run_action(self, window, action, **options):
        desktop = FakeDesktop(window)
        payload = {"action": action, "window": str(window.handle)}
        payload.update(options)
        sidecar.execute_action(desktop, payload)
        return desktop

    def test_bad_hotkey_fails_before_any_window_lookup(self):
        desktop = FakeDesktop(FakeWindow())
        with self.assertRaisesRegex(ValueError, "allowlist"):
            sidecar.execute_action(desktop, {"action": "desktop.hotkey", "window": "100", "arguments": {"keys": ["alt", "f4"]}})
        self.assertEqual(desktop.window_calls, 0)
        self.send_keys.assert_not_called()

    def test_hotkey_array_from_frontend_is_sent_once(self):
        self.run_action(FakeWindow(), "desktop.hotkey", arguments={"keys": ["ctrl", "a"]})
        self.send_keys.assert_called_once_with("{CTRL}a")

    def test_coordinate_click_clicks_once_at_the_point_and_not_at_window_center(self):
        window = FakeWindow()
        with mock.patch.object(sidecar, "root_window_at_point", return_value=window.handle):
            self.run_action(window, "desktop.click", target=self.coordinate())
        self.click.assert_called_once_with(coords=(100, 100))
        self.assertEqual(window.calls, [])  # no extra click_input on the window

    def test_coordinate_double_click_uses_one_double_click_at_the_point(self):
        window = FakeWindow()
        with mock.patch.object(sidecar, "root_window_at_point", return_value=window.handle):
            self.run_action(window, "desktop.double_click", target=self.coordinate())
        self.double_click.assert_called_once_with(coords=(100, 100))
        self.click.assert_not_called()
        self.assertEqual(window.calls, [])

    def test_coordinate_outside_the_window_is_rejected_without_clicking(self):
        window = FakeWindow(rect=(0, 0, 50, 50))
        with mock.patch.object(sidecar, "root_window_at_point", return_value=window.handle):
            with self.assertRaisesRegex(ValueError, "outside the approved window"):
                self.run_action(window, "desktop.click", target=self.coordinate())
        self.click.assert_not_called()

    def test_coordinate_covered_by_another_window_is_rejected_without_clicking(self):
        window = FakeWindow()
        with mock.patch.object(sidecar, "root_window_at_point", return_value=999):
            with self.assertRaisesRegex(ValueError, "covered by another window"):
                self.run_action(window, "desktop.click", target=self.coordinate())
        self.click.assert_not_called()

    def test_stale_or_mismatched_coordinates_are_rejected(self):
        window = FakeWindow()
        for target in (self.coordinate(screenshot_age_ms=5000), self.coordinate(screen_size=[800, 600]), self.coordinate(x=5000), {"type": "coordinate", "x": "a", "y": 1}, {"type": "coordinate"}):
            with self.subTest(target=target):
                with mock.patch.object(sidecar, "root_window_at_point", return_value=window.handle):
                    with self.assertRaises(ValueError):
                        self.run_action(window, "desktop.click", target=target)
        self.click.assert_not_called()

    def test_clicks_are_blocked_in_credential_windows(self):
        for title in ("Password Manager", "Enter credentials", "Authenticator"):
            with self.subTest(title=title):
                window = FakeWindow(title=title)
                with mock.patch.object(sidecar, "root_window_at_point", return_value=window.handle):
                    with self.assertRaisesRegex(ValueError, "password or credential"):
                        self.run_action(window, "desktop.click", target=self.coordinate())
        self.click.assert_not_called()

    def test_typing_and_hotkeys_are_still_blocked_in_credential_windows(self):
        window = FakeWindow(title="Sign in - Password")
        with self.assertRaisesRegex(ValueError, "password or credential"):
            self.run_action(window, "desktop.type", arguments={"text": "x"})
        with self.assertRaisesRegex(ValueError, "password or credential"):
            self.run_action(window, "desktop.hotkey", arguments={"keys": "esc"})
        self.send_keys.assert_not_called()

    def test_coordinate_target_cannot_be_used_to_type(self):
        window = FakeWindow()
        with self.assertRaisesRegex(ValueError, "automation_id, name, or control_type"):
            self.run_action(window, "desktop.type", arguments={"text": "x"}, target=self.coordinate())
        self.assertEqual(window.calls, [])

    def test_scroll_uses_line_unit_and_count(self):
        window = FakeWindow()
        self.run_action(window, "desktop.scroll", arguments={"amount": 3, "direction": "up"})
        self.assertEqual(window.calls, [("scroll", {"direction": "up", "amount": "line", "count": 3})])

    def test_unknown_action_is_rejected_before_anything_else(self):
        desktop = FakeDesktop(FakeWindow())
        with self.assertRaisesRegex(ValueError, "unsupported"):
            sidecar.execute_action(desktop, {"action": "desktop.format_disk", "window": "100"})
        self.assertEqual(desktop.window_calls, 0)


if __name__ == "__main__":
    unittest.main()
