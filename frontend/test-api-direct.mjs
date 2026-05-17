#!/usr/bin/env node
/**
 * Direct API test to verify backend is sending token metadata
 */

import http from 'http';

const postData = JSON.stringify({
  message: 'Hello test',
  stream: false,
});

const options = {
  hostname: 'localhost',
  port: 8080,
  path: '/api/chat',
  method: 'POST',
  headers: {
    'Content-Type': 'application/json',
    'Content-Length': Buffer.byteLength(postData),
  },
};

console.log('🔍 Testing API direct response...\n');

const req = http.request(options, (res) => {
  let data = '';

  res.on('data', (chunk) => {
    data += chunk;
  });

  res.on('end', () => {
    try {
      const response = JSON.parse(data);

      console.log('📊 API Response:');
      console.log('  - message length:', response.message?.length || 0);
      console.log('  - timestamp:', response.timestamp);
      console.log('  - metadata exists:', !!response.metadata);

      if (response.metadata) {
        console.log('\n🔍 Metadata details:');
        console.log('  - model:', response.metadata.model);
        console.log('  - provider:', response.metadata.provider);
        console.log('  - prompt_tokens:', response.metadata.prompt_tokens);
        console.log('  - completion_tokens:', response.metadata.completion_tokens);
        console.log('  - total_tokens:', response.metadata.total_tokens);
        console.log('  - ttft_ms:', response.metadata.ttft_ms);
        console.log('  - total_duration_ms:', response.metadata.total_duration_ms);
        console.log('  - tokens_per_second:', response.metadata.tokens_per_second);

        console.log('\n📋 Raw metadata object:');
        console.log(JSON.stringify(response.metadata, null, 2));

        if (response.metadata.total_tokens) {
          console.log('\n✅ SUCCESS: Backend is sending token metadata correctly!');
        } else {
          console.log('\n❌ FAILED: total_tokens is missing or falsy');
        }
      } else {
        console.log('\n❌ FAILED: No metadata in response');
      }
    } catch (error) {
      console.error('❌ Parse error:', error.message);
      console.log('Raw response:', data);
    }
  });
});

req.on('error', (error) => {
  console.error('❌ Request error:', error.message);
});

req.write(postData);
req.end();
