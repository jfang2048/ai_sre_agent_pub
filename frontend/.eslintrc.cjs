module.exports = {
    root: true,
    env: {
        browser: true,
        es2022: true,
        node: true,
    },
    parser: '@typescript-eslint/parser',
    parserOptions: {
        ecmaVersion: 'latest',
        sourceType: 'module',
    },
    plugins: ['@typescript-eslint', 'react-hooks', 'react-refresh'],
    extends: [
        'eslint:recommended',
        'plugin:@typescript-eslint/recommended',
        'plugin:react-hooks/recommended',
    ],
    ignorePatterns: ['dist/', '../web/'],
    rules: {
        '@typescript-eslint/no-explicit-any': 'off',
        'react-refresh/only-export-components': ['error', { allowConstantExport: true }],
    },
    overrides: [
        {
            files: ['**/*.test.ts', '**/*.test.tsx', 'src/test/**'],
            rules: {
                '@typescript-eslint/no-empty-function': 'off',
                '@typescript-eslint/no-non-null-assertion': 'off',
            },
        },
    ],
}
