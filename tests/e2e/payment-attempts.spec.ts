import { expect, test } from "@playwright/test";

test("payment page shows attempts exhausted state after 3 failures", async ({ page }) => {
  await page.goto("/");

  await page.getByRole("button", { name: /Flight 101/i }).click();
  await page.getByLabel(/AVAILABLE$/).first().click();
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

test("rejects different payment code before attempts exhausted", async ({ page }) => {
  await page.goto("/");

  await page.getByRole("button", { name: /Flight 101/i }).click();
  await page.getByLabel(/AVAILABLE$/).first().click();
  await page.getByRole("button", { name: /Proceed to payment/i }).click();

  const codeInput = page.locator("#payment-code");
  const submitButton = page.getByRole("button", { name: /Submit payment/i });
  const feedback = page.locator("#payment-feedback");
  const events = page.locator("#payment-events");

  await codeInput.fill("12345");
  await submitButton.click();
  await expect(page.locator("#attempts-used")).toHaveText("1 / 3", { timeout: 15000 });

  await codeInput.fill("54321");
  await expect(submitButton).toBeEnabled();
  await submitButton.click();

  await expect(feedback).toContainText("start a new payment method before using a different code");
  await expect(events).toContainText("Different code rejected");
  await expect(submitButton).toBeEnabled();
  await expect(page.locator("#order-status")).toHaveText("SEATS_HELD");
});
