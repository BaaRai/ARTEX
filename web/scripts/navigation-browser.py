import json, os, re
from pathlib import Path
from playwright.sync_api import sync_playwright, expect

base=os.environ.get('ARTEX_TEST_URL','http://127.0.0.1:18791')
results=[]
with sync_playwright() as p:
    browser=p.chromium.launch(headless=True)
    page=browser.new_page(viewport={"width":1440,"height":1000})
    page.set_default_timeout(30000)
    errors=[]
    page.on('pageerror', lambda e: errors.append(str(e)))
    def run(name, fn):
        if os.environ.get('ARTEX_TEST_FILTER') and os.environ['ARTEX_TEST_FILTER'] not in name: return
        try:
            fn()
            results.append({'name':name,'passed':True})
        except Exception as e:
            results.append({'name':name,'passed':False,'error':str(e)[:1200]})
        print(json.dumps(results[-1]),flush=True)
    def tab(name):
        return page.get_by_role('tab',name=re.compile('^'+re.escape(name)+r'(\s|\d|$)'))
    def selected(name):
        expect(tab(name)).to_have_attribute('data-state','active')
    def roundtrip(path, first, second, query):
        page.goto(base+path,wait_until='domcontentloaded',timeout=90000)
        selected(first)
        initial=page.evaluate('history.length')
        tab(second).click()
        selected(second)
        expect(page).to_have_url(re.compile(query))
        tab(second).click()
        assert page.evaluate('history.length')==initial+1
        page.go_back()
        selected(first)
        page.go_forward()
        selected(second)
        page.reload()
        selected(second)
    for args in [
        ('/function/tasks','Current tasks','Archived','tab=archived'),
        ('/function/tasks/detail?id=t-acme-web','Sessions','Findings','tab=findings'),
        ('/function/findings/detail?id=f-1','Overview','Exploration graph','tab=lineage'),
        ('/function/findings','All findings','By task','view=grouped'),
        ('/function/assets','Companies','Root domains','tab=root_domain'),
        ('/system/llm','Model profiles','Retry & backoff','tab=retry'),
        ('/system/intercept','Interception rules','Model config','tab=judge'),
        ('/system/tools','System tools','Custom tools','tab=custom'),
        ('/system/notify','Channels','Delivery log','tab=deliveries'),
    ]:
        run(args[0],lambda args=args: roundtrip(*args))
    def task_finding():
        page.goto(base+'/function/tasks/detail?id=t-acme-web&tab=findings',wait_until='domcontentloaded')
        selected('Findings')
        page.get_by_role('link',name='Details',exact=True).first.click()
        expect(page).to_have_url(re.compile('from_task=t-acme-web'))
        selected('Overview')
        page.go_back()
        selected('Findings')
        page.go_forward()
        selected('Overview')
        back=page.locator('header a[href*="tab=findings"]')
        expect(back).to_have_count(1)
        back.click()
        selected('Findings')
    run('task → finding → browser back/forward → contextual return',task_finding)
    def invalid_tab():
        page.goto(base+'/function/tasks/detail?id=t-acme-web&tab=invalid',wait_until='domcontentloaded')
        selected('Sessions')
    run('invalid tab fallback', invalid_tab)
    def chats():
        page.goto(base+'/chat?c=1',wait_until='domcontentloaded')
        one=page.locator('button[title="Double-click to rename"]').filter(has_text='Discussion on the acme admin entry point')
        two=page.locator('button[title="Double-click to rename"]').filter(has_text='Mapping the API authorization bypass surface')
        expect(one.locator('..')).to_have_class(re.compile('bg-accent text-accent-foreground'))
        two.click()
        expect(page).to_have_url(re.compile('c=2'))
        expect(two.locator('..')).to_have_class(re.compile('bg-accent text-accent-foreground'))
        page.go_back()
        expect(page).to_have_url(re.compile('c=1'))
        expect(one.locator('..')).to_have_class(re.compile('bg-accent text-accent-foreground'))
        page.go_forward()
        expect(two.locator('..')).to_have_class(re.compile('bg-accent text-accent-foreground'))
        page.reload()
        expect(two.locator('..')).to_have_class(re.compile('bg-accent text-accent-foreground'))
        page.get_by_role('button',name='New conversation',exact=True).click()
        expect(page).to_have_url(re.compile(r'c=($|&)'))
        page.go_back()
        expect(two.locator('..')).to_have_class(re.compile('bg-accent text-accent-foreground'))
    run('chat selection, draft, back, forward and reload', chats)
    def inspector(path,key):
        page.goto(base+path,wait_until='domcontentloaded')
        row=page.locator('tbody tr.cursor-pointer').first
        row.wait_for()
        row.click()
        expect(page).to_have_url(re.compile(key+'=[^&]+'))
        opened=page.url
        page.go_back()
        assert not re.search(key+'=[^&]+',page.url)
        page.go_forward()
        expect(page).to_have_url(opened)
        expect(row).to_have_class(re.compile('bg-accent'))
    for path,key in [('/function/traffic','exchange'),('/function/commands','command'),('/function/llm-records','record')]:
        run(path+' inspector back/forward',lambda path=path,key=key: inspector(path,key))
    def workspace():
        page.goto(base+'/function/workspace',wait_until='domcontentloaded')
        page.locator('tbody tr').filter(has_text='t-001').first.get_by_role('button').first.click()
        expect(page).to_have_url(re.compile('path=t-001'))
        page.go_back()
        expect(page.locator('tbody tr').filter(has_text='t-001').first).to_be_visible()
        page.go_forward()
        expect(page).to_have_url(re.compile('path=t-001'))
    run('workspace directories back/forward',workspace)
    page.screenshot(path=str(Path(__file__).with_name('mock-navigation.png')))
    browser.close()
Path(__file__).with_name('browser-results.json').write_text(json.dumps({'results':results,'page_errors':errors},indent=2))
assert all(r['passed'] for r in results), 'browser regressions failed'

assert not errors, "browser page errors occurred"
