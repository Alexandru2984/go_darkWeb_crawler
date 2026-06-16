import js from '@eslint/js'
import pluginVue from 'eslint-plugin-vue'
import globals from 'globals'

export default [
  { ignores: ['dist/**', 'node_modules/**'] },
  js.configs.recommended,
  // 'essential' = bug-catching rules only (undefined components, bad v-for
  // keys, mutating props, …). Formatting is Prettier's job, so we deliberately
  // skip the 'recommended' set's stylistic rules to keep the signal high.
  ...pluginVue.configs['flat/essential'],
  {
    languageOptions: {
      ecmaVersion: 'latest',
      sourceType: 'module',
      globals: {
        ...globals.browser,
      },
    },
    rules: {
      // The dashboard is a single large component for now; don't fail on it.
      'vue/multi-word-component-names': 'off',
      'no-unused-vars': ['warn', { argsIgnorePattern: '^_' }],
    },
  },
  {
    // Test files run under Vitest (Node + its globals).
    files: ['**/*.test.js', '**/*.spec.js'],
    languageOptions: {
      globals: { ...globals.node },
    },
  },
]
