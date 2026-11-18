# 安全策略

## 支持范围

只为最新一个 release 提供安全修复。报告前请先运行 `npm-token upgrade` 确认问题仍然存在。

## 报告问题

请通过 GitHub 的私密渠道报告，不要开公开 issue：

https://github.com/hongmengchen/npm-token/security/advisories/new

报告里请写清版本（`npm-token version`）、系统/终端、复现步骤和影响。**不要粘贴真实 token、`.npmrc` 内容、registry 地址或用户名**；用 `glpat-xxxx`、`git.example.com`、`alice` 这类假值代替。

这是个人项目，会在力所能及的范围内尽快回复，通常不超过 7 天。

## 程序的安全边界

- token 只在本机 `~/.npmrc` 和内存里；`check` 只读，输出和 JSON 都是掩码
- 写入前备份 `0600`，同目录临时文件 + rename 原子替换；新 token 被明确拒绝时自动还原
- 下载和自更新都会校验 release 里的 `checksums.txt`（SHA-256），校验失败不会覆盖原文件
- 更新检查只访问 GitHub Releases；没有遥测
