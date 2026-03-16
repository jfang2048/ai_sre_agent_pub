import { fireEvent, screen } from '@testing-library/react';
import NLQuery from '../NLQuery';
import { renderWithClient } from '@/test/utils';

describe('NLQuery', () => {
    it('hands off a trimmed query and clears the input', () => {
        const onSubmitQuery = vi.fn();

        renderWithClient(<NLQuery onSubmitQuery={onSubmitQuery} />);

        const input = screen.getByPlaceholderText(/Ask AI:/i);
        fireEvent.change(input, { target: { value: '  gpu hotspot on node-a  ' } });
        fireEvent.submit(input.closest('form') as HTMLFormElement);

        expect(onSubmitQuery).toHaveBeenCalledWith('gpu hotspot on node-a');
        expect(input).toHaveValue('');
    });

    it('ignores blank submissions', () => {
        const onSubmitQuery = vi.fn();

        renderWithClient(<NLQuery onSubmitQuery={onSubmitQuery} />);

        const input = screen.getByPlaceholderText(/Ask AI:/i);
        fireEvent.change(input, { target: { value: '   ' } });
        fireEvent.submit(input.closest('form') as HTMLFormElement);

        expect(onSubmitQuery).not.toHaveBeenCalled();
    });
});
