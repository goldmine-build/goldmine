import './index.js';

import { $, $$ } from 'common-sk/modules/dom';
import { setUpElementUnderTest } from '../test_util';

describe('pagination-sk', () => {
  const newInstance = setUpElementUnderTest('pagination-sk');

  let paginationSk;
  beforeEach(() => {
    paginationSk = newInstance((el) => {
      el.setAttribute('offset', '0');
      el.setAttribute('total', '127');
      el.setAttribute('page_size', '20');
    });
  });

  describe('html layout', () => {
    it('has three buttons', () => {
      const btns = $('button', paginationSk);
      expect(btns.length).to.equal(3);
      // backward
      expect(btns[0].hasAttribute('disabled')).to.be.true;
      // forward
      expect(btns[1].hasAttribute('disabled')).to.be.false;
      // forward+5
      expect(btns[2].hasAttribute('disabled')).to.be.false;

      paginationSk.offset = 20;
      paginationSk._render();
      expect(btns[0].hasAttribute('disabled')).to.be.false;
      expect(btns[1].hasAttribute('disabled')).to.be.false;
      expect(btns[2].hasAttribute('disabled')).to.be.false;

      paginationSk.offset = 40;
      paginationSk._render();
      expect(btns[0].hasAttribute('disabled')).to.be.false;
      expect(btns[1].hasAttribute('disabled')).to.be.false;
      expect(btns[2].hasAttribute('disabled')).to.be.true;

      paginationSk.offset = 120;
      paginationSk._render();
      expect(btns[0].hasAttribute('disabled')).to.be.false;
      expect(btns[1].hasAttribute('disabled')).to.be.true;
      expect(btns[2].hasAttribute('disabled')).to.be.true;
    });

    it('displays the page count', () => {
      const cnt = $$('.counter', paginationSk);
      expect(cnt).to.not.be.null;
      expect(cnt.textContent).to.have.string('page 1');
    });

    it('has several properties', () => {
      expect(paginationSk.total).to.equal(127);
      expect(paginationSk.offset).to.equal(0);
      expect(paginationSk.page_size).to.equal(20);
    });
  });// end describe('html layout')

  describe('paging behavior', () => {
    it('creates page events', (done) => {
      paginationSk.offset = 20;
      paginationSk._render();
      const btns = $('button', paginationSk);
      expect(btns.length).to.equal(3);
      const bck =  btns[0];
      const fwd =  btns[1];
      const pls5 = btns[2];

      let d = 0;
      const deltas = [1 ,-1, 5];
      paginationSk.addEventListener('page-changed', (e) => {
        expect(e.detail.delta).to.equal(deltas[d]);
        d++;
        if (d === 3) {
          done();
        }
      });
      fwd.click();
      bck.click();
      pls5.click();
    });
  }); // end describe('paging behavior')
});
