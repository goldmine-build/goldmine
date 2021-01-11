package main

import (
	"time"

	"github.com/GeertJohan/go.rice/embedded"
)

func init() {

	// define files
	file2 := &embedded.EmbeddedFile{
		Filename:    "file-demo.html",
		FileModTime: time.Unix(1603993235, 0),

		Content: string("<!DOCTYPE html>\n<html>\n<head>\n  <title>{{.ElementName}}</title>\n  <meta charset=\"utf-8\" />\n  <meta http-equiv=\"X-UA-Compatible\" content=\"IE=edge\">\n  <meta name=\"viewport\" content=\"width=device-width, initial-scale=1.0\">\n</head>\n<body>\n  <h1>{{.ElementName}}</h1>\n  <{{.ElementName}}></{{.ElementName}}>\n\n  <h2>Events</h2>\n  <pre id=events></pre>\n</body>\n</html>\n"),
	}
	file3 := &embedded.EmbeddedFile{
		Filename:    "file-demo.ts",
		FileModTime: time.Unix(1603993235, 0),

		Content: string("import './index';\n\ndocument\n  .querySelector('{{.ElementName}}')!\n  .addEventListener('some-event-name', (e) => {\n    document.querySelector('#events')!.textContent = JSON.stringify(\n      e,\n      null,\n      '  '\n    );\n  });\n"),
	}
	file4 := &embedded.EmbeddedFile{
		Filename:    "file.scss",
		FileModTime: time.Unix(1603993235, 0),

		Content: string("{{.ElementName}} {\n}\n"),
	}
	file5 := &embedded.EmbeddedFile{
		Filename:    "file.ts",
		FileModTime: time.Unix(1603993235, 0),

		Content: string("/**\n * @module modules/{{.ElementName}}\n * @description <h2><code>{{.ElementName}}</code></h2>\n *\n * @evt\n *\n * @attr\n *\n * @example\n */\nimport { define } from 'elements-sk/define';\nimport { html } from 'lit-html';\nimport { ElementSk } from '../../../infra-sk/modules/ElementSk';\n\nexport class {{.ClassName}} extends ElementSk {\n  private static template = (ele: {{.ClassName}}) =>\n    html`<h3>Hello world</h3>`;\n\n  constructor() {\n    super({{.ClassName}}.template);\n  }\n\n  connectedCallback() {\n    super.connectedCallback();\n    this._render();\n  }\n};\n\ndefine('{{.ElementName}}', {{.ClassName}});\n"),
	}
	file6 := &embedded.EmbeddedFile{
		Filename:    "file_puppeteer_test.ts",
		FileModTime: time.Unix(1610144663, 0),

		Content: string("import * as path from 'path';\nimport { expect } from 'chai';\nimport {\n  loadCachedTestBed, takeScreenshot, TestBed,\n} from '../../../puppeteer-tests/util';\n\ndescribe('{{.ElementName}}', () => {\n  let testBed: TestBed;\n  before(async () => {\n    testBed = await loadCachedTestBed(\n        path.join(__dirname, '..', '..', 'webpack.config.ts')\n    );\n  });\n\n  beforeEach(async () => {\n    await testBed.page.goto(`${testBed.baseUrl}/dist/{{.ElementName}}.html`);\n    await testBed.page.setViewport({ width: 400, height: 550 });\n  });\n\n  it('should render the demo page (smoke test)', async () => {\n    expect(await testBed.page.$$('{{.ElementName}}')).to.have.length(1);\n  });\n\n  describe('screenshots', () => {\n    it('shows the default view', async () => {\n      await takeScreenshot(testBed.page, '{{.AppName}}', '{{.ElementName}}');\n    });\n  });\n});\n"),
	}
	file7 := &embedded.EmbeddedFile{
		Filename:    "file_test.ts",
		FileModTime: time.Unix(1603993235, 0),

		Content: string("import './index';\nimport { {{.ClassName }} } from './{{.ElementName}}';\n\nimport { setUpElementUnderTest } from '../../../infra-sk/modules/test_util';\nimport { expect } from 'chai';\n\ndescribe('{{.ElementName}}', () => {\n  const newInstance = setUpElementUnderTest<{{.ClassName}}>('{{.ElementName}}');\n\n  let element: {{.ClassName}};\n  beforeEach(() => {\n    element = newInstance((el: {{.ClassName}}) => {\n      // Place here any code that must run after the element is instantiated but\n      // before it is attached to the DOM (e.g. property setter calls,\n      // document-level event listeners, etc.).\n    });\n  });\n\n  describe('some action', () => {\n    it('some result', () => {});\n      expect(element).to.not.be.null;\n  });\n});\n"),
	}
	file8 := &embedded.EmbeddedFile{
		Filename:    "index.ts",
		FileModTime: time.Unix(1603993235, 0),

		Content: string("import './{{.ElementName}}';\nimport './{{.ElementName}}.scss';\n"),
	}

	// define dirs
	dir1 := &embedded.EmbeddedDir{
		Filename:   "",
		DirModTime: time.Unix(1603993235, 0),
		ChildFiles: []*embedded.EmbeddedFile{
			file2, // "file-demo.html"
			file3, // "file-demo.ts"
			file4, // "file.scss"
			file5, // "file.ts"
			file6, // "file_puppeteer_test.ts"
			file7, // "file_test.ts"
			file8, // "index.ts"

		},
	}

	// link ChildDirs
	dir1.ChildDirs = []*embedded.EmbeddedDir{}

	// register embeddedBox
	embedded.RegisterEmbeddedBox(`templates`, &embedded.EmbeddedBox{
		Name: `templates`,
		Time: time.Unix(1603993235, 0),
		Dirs: map[string]*embedded.EmbeddedDir{
			"": dir1,
		},
		Files: map[string]*embedded.EmbeddedFile{
			"file-demo.html":         file2,
			"file-demo.ts":           file3,
			"file.scss":              file4,
			"file.ts":                file5,
			"file_puppeteer_test.ts": file6,
			"file_test.ts":           file7,
			"index.ts":               file8,
		},
	})
}
