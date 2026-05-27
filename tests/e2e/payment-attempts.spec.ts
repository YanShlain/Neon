import { expect, test } from "@playwright/test";

test("payment page shows attempts exhausted state after 3 failures", async ({ page }) => {
  await page.goto("/");

  await page.getByRole("button", { name: /Flight 101/i }).click();
  await page.getByLabel("1A AVAILABLE").click();
  await page.getByRole("button", { name: /Proceed to payment/i }).click();

  const codeInput = page.locator("#payment-code");
  const submitButton = page.getByRole("button", { name: /Submit payment/i });
  const feedback = page.locator("#payment-feedback");

  for (let i = 0; i < 3; i++) {
    await codeInput.fill("42315");
    await submitButton.click();
    await expect(page.locator("#attempts-used")).toHaveText(`${i + 1} / 3`, { timeout: 15000 });
  }
  await expect(submitButton).toBeDisabled();
  await expect(feedback).toContainText("Attempts exhausted for this code");

  await codeInput.fill("77777");
  await expect(submitButton).toBeEnabled();

  await submitButton.click();
  await expect(feedback).not.toContainText("Attempts exhausted for this code");
});
