#!/usr/bin/env node
/**
 * Quick metadata display diagnostic for US-015
 * Tests if markRaw() fix resolved Vue reactivity issue
 */

import { chromium } from 'playwright';

async function testMetadataDisplay() {
  const browser = await chromium.launch({ headless: true });
  const context = await browser.newContext();
  const page = await context.newPage();

  try {
    console.log('🔍 Testing metadata display with markRaw() fix...\n');

    // Navigate to the work page
    await page.goto('http://localhost:8080/work');
    await page.waitForTimeout(2000);

    // Send a test message
    const input = page.locator('[data-testid="chat-input"]');
    await input.fill('Hello, test message for metadata');

    const sendButton = page.locator('[data-testid="send-button"]');
    await sendButton.click();

    console.log('✅ Sent test message, waiting for response...');

    // Wait for AI response with metadata
    await page.waitForSelector('[data-testid="ai-message"]', { timeout: 30000 });
    await page.waitForTimeout(2000);

    // Check for metadata element
    const metadataElements = await page.locator('[data-testid="message-metadata"]').all();

    if (metadataElements.length === 0) {
      console.log('❌ No metadata elements found');
      return false;
    }

    const lastMetadata = metadataElements[metadataElements.length - 1];
    const metadataText = await lastMetadata.textContent();

    console.log('📊 Metadata text:', metadataText);

    // Check for debug output
    const debugElements = await page.locator('.text-orange-500').all();
    if (debugElements.length > 0) {
      const debugText = await debugElements[debugElements.length - 1].textContent();
      console.log('🟠 DEBUG:', debugText);
    }

    // Check if metadata has token information
    const hasTokens = metadataText.includes('tok') || metadataText.includes('?/?');
    const hasModel = metadataText.includes('glm') || metadataText.includes('gpt');

    console.log('\n📈 Results:');
    console.log('  - Has model info:', hasModel);
    console.log('  - Has token info:', hasTokens);
    console.log('  - Token format valid:', hasTokens && !metadataText.includes('?/?'));

    if (hasModel && hasTokens && !metadataText.includes('?/?')) {
      console.log('\n✅ SUCCESS: Metadata display working correctly!');
      return true;
    } else {
      console.log('\n❌ FAILED: Metadata still not displaying correctly');
      console.log('   Expected: model name + token counts (e.g., "glm-4-flash • ⚡ 15/230 tok")');
      console.log('   Got:', metadataText);
      return false;
    }

  } catch (error) {
    console.error('❌ Test error:', error.message);
    return false;
  } finally {
    await context.close();
    await browser.close();
  }
}

// Run test
testMetadataDisplay().then(success => {
  process.exit(success ? 0 : 1);
});
