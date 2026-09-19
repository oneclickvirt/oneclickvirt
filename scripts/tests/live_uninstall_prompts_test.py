#!/usr/bin/env python3
"""Prompt parser regressions; real node acceptance is a separate driver."""
import unittest

from live_incus_uninstall_test import UninstallPrompts


class PromptTests(unittest.TestCase):
    def test_lxd_needs_both_confirmations(self):
        dialogue, sent = UninstallPrompts("lxd"), []
        dialogue.feed("Confirm continue? (y/n) [n]:", sent.append)
        self.assertEqual(sent, ["y\n"])
        self.assertFalse(dialogue.complete)
        dialogue.feed("Also remove backing storage files? (y/n) [n]:", sent.append)
        self.assertEqual(sent, ["y\n", "y\n"])
        self.assertTrue(dialogue.complete)

    def test_prompts_split_at_every_character(self):
        dialogue, sent = UninstallPrompts("lxd"), []
        for character in "Confirm continue? (y/n)\r\nAlso remove backing storage files? (y/n)":
            dialogue.feed(character, sent.append)
        self.assertTrue(dialogue.complete)
        self.assertEqual(sent, ["y\n", "y\n"])

    def test_coalesced_prompts_and_repeated_output(self):
        dialogue, sent = UninstallPrompts("lxd"), []
        text = "Confirm continue? (y/n)\nAlso remove backing storage files? (y/n)"
        dialogue.feed(text, sent.append)
        dialogue.feed(text, sent.append)
        self.assertEqual(sent, ["y\n", "y\n"])

    def test_incus_has_one_confirmation(self):
        dialogue, sent = UninstallPrompts("incus"), []
        dialogue.feed("type 'yes' to continue", sent.append)
        self.assertTrue(dialogue.complete)
        self.assertEqual(sent, ["yes\n"])

    def test_unrelated_output_is_bounded_and_does_not_complete(self):
        dialogue, sent = UninstallPrompts("lxd"), []
        dialogue.feed("x" * 100000, sent.append)
        self.assertEqual(len(dialogue.buffer), 16384)
        self.assertFalse(dialogue.complete)
        self.assertEqual(sent, [])


if __name__ == "__main__":
    unittest.main(verbosity=2)
