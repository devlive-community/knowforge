#!/usr/bin/env python3
"""生成图标选择器使用的 Font Awesome 图标目录（lib/fa-icons.json）。

从 @fortawesome/fontawesome-free 的 metadata 中取免费的 solid 图标与搜索词，并把 FA 的分类归并为
选择器的几个分组。升级 Font Awesome 后重新运行：python3 scripts/gen_fa_icons.py
"""
import json
import os

import yaml

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
META = os.path.join(ROOT, 'node_modules', '@fortawesome', 'fontawesome-free', 'metadata')
OUT = os.path.join(ROOT, 'lib', 'fa-icons.json')

# 选择器分组 → FA 分类
GROUPS = {
    'files': ['files', 'writing', 'business'],
    'users': ['users-people', 'social'],
    'system': ['devices-hardware', 'coding', 'security', 'connectivity', 'toggle'],
    'media': ['photos-images', 'film-video', 'music-audio', 'media-playback'],
    'editing': ['editing', 'text-formatting', 'design'],
    'education': ['education', 'science', 'mathematics', 'charts-diagrams'],
}

# 常用：分类、标签、会员、等级等最常选的图标（按展示顺序）
COMMON = [
    'folder', 'folder-open', 'file-lines', 'book', 'book-open', 'bookmark', 'tag', 'tags', 'box', 'database',
    'gear', 'user', 'users', 'star', 'heart', 'bell', 'house', 'table-cells-large', 'crown', 'gem',
    'trophy', 'medal', 'award', 'fire', 'bolt', 'rocket', 'lightbulb', 'graduation-cap', 'code', 'terminal',
    'globe', 'compass', 'flag', 'shield-halved', 'key', 'lock', 'image', 'music', 'video', 'pen',
    'palette', 'chart-line', 'calendar', 'clock', 'comments', 'envelope', 'link', 'leaf', 'hashtag', 'circle-info',
]


def main():
    icons = yaml.safe_load(open(os.path.join(META, 'icons.yml'), encoding='utf-8'))
    categories = yaml.safe_load(open(os.path.join(META, 'categories.yml'), encoding='utf-8'))
    solid = {name: meta for name, meta in icons.items() if 'solid' in (meta.get('styles') or [])}
    names = sorted(solid)
    out_icons = []
    for name in names:
        meta = solid[name]
        terms = [meta.get('label') or '']
        terms += (meta.get('search') or {}).get('terms') or []
        terms += (meta.get('aliases') or {}).get('names') or []
        text = ' '.join(sorted({str(t).lower() for t in terms if t}))
        out_icons.append([name, text])
    groups = {'common': [n for n in COMMON if n in solid]}
    for key, cats in GROUPS.items():
        seen = []
        for cat in cats:
            for n in (categories.get(cat) or {}).get('icons') or []:
                if n in solid and n not in seen:
                    seen.append(n)
        groups[key] = seen
    missing = [n for n in COMMON if n not in solid]
    if missing:
        raise SystemExit('常用图标不存在：' + ', '.join(missing))
    with open(OUT, 'w', encoding='utf-8') as f:
        json.dump({'icons': out_icons, 'groups': groups}, f, ensure_ascii=False, separators=(',', ':'))
        f.write('\n')
    print(f'{len(out_icons)} icons, groups: ' + ', '.join(f'{k}={len(v)}' for k, v in groups.items()))


if __name__ == '__main__':
    main()
