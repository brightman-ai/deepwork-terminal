#!/usr/bin/env node
/**
 * Test that captures browser console logs to see metadata flow
 */

import { chromium } from 'playwright';

async function testWithConsole() {
  const browser = await chromium.launch({ headless: true });
  const context = await browser.newContext();
  const page = await context.newPage();

  // Capture all console messages
  const consoleLogs = [];
  page.on('console', msg => {
    const text = msg.text();
    consoleLogs.push(text);
    console.log(`[Console] ${text}`);
  });

  try {
    await page.goto('http://localhost:8080/work');
    await page.waitForTimeout(2000);

    // Send a test message
    const input = page.locator('[data-testid="chat-input"]');
    await input.fill('Test message');

    const sendButton = page.locator('[data-testid="send-button"]');
    await sendButton.click();

    console.log('\n===Waiting for AI response...===\n');

    // Wait for AI response
    await page.waitForSelector('[data-testid="ai-message"]', { timeout: 30000 });
    await page.waitForTimeout(3000); // Give time for all logs

    console.log('\n=== All Console Logs ===\n');
    consoleLogs.forEach(log => console.log(log));

  } catch (error) {
    console.error('Error:', error.message);
  } finally {
    await context.close();
    await browser.close();
  }
}

testWithConsole();
