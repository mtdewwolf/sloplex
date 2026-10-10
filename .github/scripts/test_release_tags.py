import unittest

from release_tags import release


class ReleaseTagsTest(unittest.TestCase):
    def tags(self, version, *existing):
        return release(version, list(existing))["tags"]

    def test_first_release(self):
        out = release("v0.1.0", [])
        self.assertEqual(out, {"version": "0.1.0", "tag": "v0.1.0", "prerelease": "false",
                               "latest": "true", "tags": "0.1.0 0.1 latest"})

    def test_newest_release_moves_both(self):
        self.assertEqual(self.tags("0.2.0", "v0.1.0", "v0.1.1"), "0.2.0 0.2 latest")
        self.assertEqual(self.tags("0.1.2", "v0.1.0", "v0.1.1"), "0.1.2 0.1 latest")

    def test_own_tag_already_pushed(self):
        # A tag push lists the release's own tag among the existing ones.
        self.assertEqual(self.tags("0.2.0", "v0.1.0", "v0.2.0"), "0.2.0 0.2 latest")

    def test_patch_on_an_older_line(self):
        out = release("0.1.5", ["v0.1.4", "v0.2.0"])
        self.assertEqual((out["tags"], out["latest"]), ("0.1.5 0.1", "false"))
        self.assertEqual(self.tags("0.1.3", "v0.1.4", "v0.2.0"), "0.1.3")

    def test_numeric_not_text_order(self):
        self.assertEqual(self.tags("0.10.0", "v0.9.0", "v0.2.0"), "0.10.0 0.10 latest")
        self.assertEqual(self.tags("0.9.1", "v0.10.0"), "0.9.1 0.9")

    def test_prereleases(self):
        out = release("v0.3.0-rc.1", ["v0.2.0"])
        self.assertEqual((out["tags"], out["prerelease"], out["latest"]), ("0.3.0-rc.1", "true", "false"))
        # Prereleases and unrelated tags never hold back a stable release.
        self.assertEqual(self.tags("0.2.1", "v0.2.0", "v0.3.0-rc.1", "nightly", "v1"), "0.2.1 0.2 latest")

    def test_rejects(self):
        for bad in ["", "v", "1.2", "1.2.3.4", "01.2.3", "1.2.3+build", "1.2.3-", "1.2.3-01", "latest",
                    "1.2.3-rc_1", "x1.2.3", "1.2.3-" + "a" * 120]:
            with self.assertRaises(ValueError, msg=bad):
                release(bad, [])


if __name__ == "__main__":
    unittest.main()
