import type { Locator, Page } from '@playwright/test';
import {
  awsTest, expect, m4FreshTest, m4Test, signinDoneTest, type Gateway,
} from './fixture';

// Each test starts its own webfake, and a cold build of it counts towards the
// test's own timeout.
awsTest.setTimeout(60_000);
m4FreshTest.setTimeout(60_000);
m4Test.setTimeout(60_000);
signinDoneTest.setTimeout(60_000);

const signIn = (page: Page) => page.getByRole('dialog', { name: 'Sign in', exact: true });

// openFromModels walks the path a user takes from the HUD: the model selector
// carries the way to a provider this tab is not signed in to.
async function openFromModels(page: Page, gateway: Gateway): Promise<Locator> {
  await page.goto(gateway.launch);
  await expect(page.getByRole('status').filter({ hasText: 'Linked' })).toBeVisible();
  await page.getByRole('button', { name: 'Model: Model One. Switch model' }).click();
  const models = page.getByRole('dialog', { name: 'Models' });
  await models.getByRole('button', { name: 'Sign in to another provider' }).click();
  const dialog = signIn(page);
  await expect(dialog).toBeVisible();
  return dialog;
}

async function startCopilot(dialog: Locator) {
  await dialog.getByRole('option', { name: 'GitHub Copilot' }).click();
  await dialog.getByRole('button', { name: 'Start sign-in' }).click();
  await expect(dialog.locator('.signin__code-value')).toHaveText('WDJB-MJHT');
}

m4FreshTest('signs in with an API key from the model selector', async ({ page, gateway }) => {
  // The answer is held so the field can be read before the request settles.
  let release!: () => void;
  const held = new Promise<void>((resolve) => { release = resolve; });
  await page.route('**/api/auth/provider/key', async (route) => {
    await held;
    await route.continue();
  });

  const dialog = await openFromModels(page, gateway);
  await dialog.getByRole('option', { name: 'Anthropic' }).click();
  await dialog.getByLabel('API key', { exact: true }).fill('sk-valid');
  await page.keyboard.press('Enter');
  await expect(dialog.getByLabel('API key', { exact: true })).toHaveValue('');

  release();
  await expect(dialog).toBeHidden();
  await expect(page.locator('.toast').filter({ hasText: 'Signed in to Anthropic.' })).toBeVisible();
});

m4Test('refuses a bad key without echoing it', async ({ page, gateway }) => {
  const dialog = await openFromModels(page, gateway);
  await dialog.getByRole('option', { name: 'Anthropic' }).click();
  await dialog.getByLabel('API key', { exact: true }).fill('sk-wrong');
  await page.keyboard.press('Enter');

  await expect(dialog.getByRole('alert')).toContainText('The provider did not accept this key.');
  await expect(dialog).toBeVisible();
  // The field is cleared on submit and the key is nowhere else either.
  await expect(dialog.getByLabel('API key', { exact: true })).toHaveValue('');
  expect(await page.content()).not.toContain('sk-wrong');
  expect(await page.evaluate(() => JSON.stringify({ ...sessionStorage, ...localStorage }))).not.toContain('sk-wrong');
});

m4FreshTest('shows the Copilot device code and cancels', async ({ page, gateway }) => {
  const dialog = await openFromModels(page, gateway);
  await startCopilot(dialog);

  const link = dialog.getByRole('link', { name: 'Open the GitHub Copilot sign-in page' });
  await expect(link).toHaveAttribute('href', 'https://github.com/login/device');
  await expect(link).toHaveAttribute('target', '_blank');
  expect(await link.getAttribute('rel')).toContain('noopener');

  await dialog.getByRole('button', { name: 'Cancel sign-in' }).click();
  await expect(dialog.getByRole('alert')).toContainText('Sign-in cancelled.');
  // The list is where the user chooses the next thing to try.
  await expect(page.locator('#signin-option-copilot')).toBeFocused();
});

signinDoneTest('completes a device sign-in', async ({ page, gateway }) => {
  const dialog = await openFromModels(page, gateway);
  // The flow finishes on its own three seconds after it starts, so the code
  // does not have to be on screen for the sign-in to complete.
  await dialog.getByRole('option', { name: 'GitHub Copilot' }).click();
  await dialog.getByRole('button', { name: 'Start sign-in' }).click();

  await expect(dialog.locator('.signin__done')).toHaveText('Signed in to GitHub Copilot.', { timeout: 15_000 });
  await expect(page.locator('.toast').filter({ hasText: 'Signed in to GitHub Copilot.' })).toBeVisible();
});

m4FreshTest('a reload keeps the running sign-in', async ({ page, gateway }) => {
  const dialog = await openFromModels(page, gateway);
  await startCopilot(dialog);

  await page.reload();
  await expect(page.getByRole('status').filter({ hasText: 'Linked' })).toBeVisible();

  // The palette is the other way back to the dialog.
  await page.getByLabel('Transmit to crush').focus();
  await page.keyboard.press('Control+K');
  const palette = page.getByRole('dialog', { name: 'Command palette' });
  await palette.getByRole('option', { name: /Sign in to a provider/ }).click();

  // The gateway still runs the flow, so its code comes back with the dialog.
  await expect(signIn(page).locator('.signin__code-value')).toHaveText('WDJB-MJHT');
});

awsTest('shows the AWS SSO card', async ({ page, gateway }) => {
  await page.goto(gateway.launch);
  const card = page.locator('.aws-sso');
  await expect(card.getByRole('heading', { name: 'AWS sign-in' })).toBeVisible();
  await expect(card).toContainText('aws sso login --profile dev');
  await expect(card.getByRole('link', { name: 'Open the AWS sign-in page' })).toHaveAttribute(
    'href', 'https://device.sso.us-east-1.amazonaws.com/?user_code=ABCD',
  );
});

// The side answer joins the deck column, so --deck-h must measure both: the
// card sits above the column at a desktop width and a phone one, never over
// the answer or the prompt box.
awsTest('keeps the AWS SSO card above an open side answer', async ({ page, gateway }) => {
  await page.goto(gateway.launch);
  await expect(page.getByRole('status').filter({ hasText: 'Linked' })).toBeVisible();

  await page.getByLabel('Transmit to crush').focus();
  await page.keyboard.press('Control+K');
  const palette = page.getByRole('dialog', { name: 'Command palette' });
  await palette.getByRole('combobox', { name: 'Command palette' }).fill('side question');
  // Enter only runs the item once the list has answered, so wait for it.
  await expect(palette.getByRole('option', { name: /Ask side question/ })).toBeVisible();
  await page.keyboard.press('Enter');
  const form = palette.getByRole('form', { name: 'Arguments for Ask side question' });
  await form.getByLabel('Question (required)').fill('How does the parser work?');
  await form.getByRole('button', { name: 'Run' }).click();
  await expect(page.getByLabel('Side answer')).toContainText('The parser uses a recursive descent.');

  const card = page.locator('.aws-sso');
  await expect(card.getByRole('link', { name: 'Open the AWS sign-in page' })).toBeVisible();
  // top and bottom are the viewport edges of a selector's box; a missing box
  // reads as off the offending side so a poll keeps failing.
  const edge = async (selector: string, side: 'top' | 'bottom') => {
    const box = await page.locator(selector).boundingBox();
    if (box === null) return side === 'top' ? Number.NEGATIVE_INFINITY : Number.POSITIVE_INFINITY;
    return side === 'top' ? box.y : box.y + box.height;
  };

  for (const viewport of [{ width: 1440, height: 860 }, { width: 390, height: 844 }]) {
    await page.setViewportSize(viewport);
    // --deck-h lands on the next resize callback, so poll rather than race it.
    await expect.poll(async () => (await edge('.aws-sso', 'bottom')) - (await edge('.side-answer', 'top')), {
      message: `the card clears the side answer at ${viewport.width}px`,
    }).toBeLessThanOrEqual(0.5);
    await expect.poll(async () => (await edge('.aws-sso', 'bottom')) - (await edge('form.deck', 'top')), {
      message: `the card clears the deck at ${viewport.width}px`,
    }).toBeLessThanOrEqual(0.5);
    // Above the card is the HUD strip: the card starts below it and its title
    // stays on screen, so the height limit scrolls the body of the card and
    // never its head.
    await expect.poll(async () => (await edge('.aws-sso', 'top')) - (await edge('.hud', 'bottom')), {
      message: `the card starts below the HUD at ${viewport.width}px`,
    }).toBeGreaterThanOrEqual(-0.5);
    await expect(card.getByRole('heading', { name: 'AWS sign-in' })).toBeVisible();
  }
});
