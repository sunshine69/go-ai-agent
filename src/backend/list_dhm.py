import os

cat_dir = os.path.join('resources/rag_documents', 'dhm')
print('cat_dir:', cat_dir)
print()
for dirpath, dirnames, filenames in os.walk(cat_dir):
    for filename in sorted(filenames):
        ext = os.path.splitext(filename)[1]
        relpath = os.path.relpath(os.path.join(dirpath, filename), cat_dir)
        print(f'{relpath} [{ext}]')
